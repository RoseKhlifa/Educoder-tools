package educoder

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestGraphicPublicAddressAndHostKeyPinning(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.169.254", "100.64.1.1", "::1", "fc00::1", "2001:db8::1", "0.0.0.0"} {
		if graphicPublicIP(net.ParseIP(address)) {
			t.Errorf("accepted local/reserved %s", address)
		}
	}
	if !graphicPublicIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address rejected")
	}
	transport := NewSSHGraphicTransport(GraphicSSHOptions{})
	_, privateA, _ := ed25519.GenerateKey(rand.Reader)
	signerA, _ := ssh.NewSignerFromKey(privateA)
	_, privateB, _ := ed25519.GenerateKey(rand.Reader)
	signerB, _ := ssh.NewSignerFromKey(privateB)
	if err := transport.hostKey("example:2222", nil, signerA.PublicKey()); err != nil {
		t.Fatal(err)
	}
	if err := transport.hostKey("example:2222", nil, signerA.PublicKey()); err != nil {
		t.Fatal(err)
	}
	if err := transport.hostKey("example:2222", nil, signerB.PublicKey()); err == nil {
		t.Fatal("changed host key accepted")
	}
	for _, host := range []string{"127.0.0.1", "localhost", "https://ssh.educoder.net/path"} {
		if _, err := transport.Open(context.Background(), GraphicConnection{Host: host, Port: 22, Username: "headless", Password: "p"}); err == nil {
			t.Errorf("opened invalid host %s", host)
		}
	}
}

// A real local SSH and SFTP server verifies the production transport. Only its
// network dial is redirected; host validation, SSH key verification, SFTP CAS,
// atomic replacement, and command construction all run unchanged.
func TestGraphicSSHExactPathWriteCASAndSymlinkRejection(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(private)
	config := &ssh.ServerConfig{PasswordCallback: func(c ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if c.User() != "headless" || string(password) != "ephemeral" {
			return nil, errors.New("bad fixture authentication")
		}
		return nil, nil
	}}
	config.AddHostKey(signer)
	handlers := sftp.InMemHandler()
	var commands []string
	var mu sync.Mutex
	go func() {
		for {
			raw, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				connection, channels, requests, e := ssh.NewServerConn(raw, config)
				if e != nil {
					raw.Close()
					return
				}
				defer connection.Close()
				go ssh.DiscardRequests(requests)
				for newChannel := range channels {
					if newChannel.ChannelType() != "session" {
						_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported")
						continue
					}
					channel, reqs, e := newChannel.Accept()
					if e != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for request := range reqs {
							switch request.Type {
							case "subsystem":
								var subsystem struct{ Name string }
								_ = ssh.Unmarshal(request.Payload, &subsystem)
								if subsystem.Name != "sftp" {
									_ = request.Reply(false, nil)
									return
								}
								_ = request.Reply(true, nil)
								server := sftp.NewRequestServer(channel, handlers)
								_ = server.Serve()
								_ = server.Close()
								return
							case "exec":
								var exec struct{ Command string }
								_ = ssh.Unmarshal(request.Payload, &exec)
								mu.Lock()
								commands = append(commands, exec.Command)
								mu.Unlock()
								_ = request.Reply(true, nil)
								if strings.Contains(exec.Command, "git rev-parse HEAD") {
									_, _ = io.WriteString(channel, graphicCommitFixture+"\n")
								}
								_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
								return
							default:
								_ = request.Reply(false, nil)
							}
						}
					}()
				}
			}()
		}
	}()
	transport := NewSSHGraphicTransport(GraphicSSHOptions{})
	transport.lookupIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	transport.dial = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
	}
	session, err := transport.Open(context.Background(), GraphicConnection{Host: "ssh.educoder.net", Port: 2222, Username: "headless", Password: "ephemeral"})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	remote := session.(*sshGraphicSession)
	if err = remote.files.MkdirAll(GraphicWorkspace); err != nil {
		t.Fatal(err)
	}
	file := "answer/用例图.mdj"
	content, exists, err := session.ReadFile(context.Background(), file)
	if err != nil || exists || len(content) > 0 {
		t.Fatalf("newfile %q %v %v", content, exists, err)
	}
	commit, err := session.SaveAndCommit(context.Background(), file, graphicHash(nil), false, []byte(graphicProject))
	if err != nil || commit != graphicCommitFixture {
		t.Fatalf("save %s %v", commit, err)
	}
	actual, exists, err := session.ReadFile(context.Background(), file)
	if err != nil || !exists || string(actual) != graphicProject {
		t.Fatalf("saved file mismatch %q %v", actual, err)
	}
	if _, err = session.SaveAndCommit(context.Background(), file, graphicHash(nil), false, []byte(graphicProject)); !errors.Is(err, ErrGraphicChanged) {
		t.Fatalf("stale baseline accepted %v", err)
	}
	if err = remote.files.Symlink(GraphicWorkspace+"/answer", GraphicWorkspace+"/linked"); err != nil {
		t.Fatal(err)
	}
	if _, _, err = session.ReadFile(context.Background(), "linked/用例图.mdj"); err == nil {
		t.Fatal("symlink directory accepted")
	}
	mu.Lock()
	defer mu.Unlock()
	commits := 0
	for _, command := range commands {
		if strings.Contains(command, "git add") {
			commits++
			if strings.Contains(command, "git add -A") || !strings.Contains(command, "git add -- 'answer/用例图.mdj'") || !strings.Contains(command, "commit --only") || !strings.Contains(command, "core.hooksPath=/dev/null") {
				t.Errorf("unsafe command %s", command)
			}
		}
	}
	if commits != 1 {
		t.Fatalf("got %d git commits", commits)
	}
}
