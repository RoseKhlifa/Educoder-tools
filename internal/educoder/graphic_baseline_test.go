package educoder

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// graphicSFTPFixture serves an in-memory SFTP workspace over SSH and answers
// the git commands a save runs, like the recorded environment.
func graphicSFTPFixture(t *testing.T) (GraphicSession, *sshGraphicSession) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	_, private, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(private)
	config := &ssh.ServerConfig{PasswordCallback: func(ssh.ConnMetadata, []byte) (*ssh.Permissions, error) { return nil, nil }}
	config.AddHostKey(signer)
	handlers := sftp.InMemHandler()
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
					channel, reqs, e := newChannel.Accept()
					if e != nil {
						continue
					}
					go func() {
						defer channel.Close()
						for request := range reqs {
							switch request.Type {
							case "subsystem":
								_ = request.Reply(true, nil)
								server := sftp.NewRequestServer(channel, handlers)
								_ = server.Serve()
								_ = server.Close()
								return
							case "exec":
								var exec struct{ Command string }
								_ = ssh.Unmarshal(request.Payload, &exec)
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
	t.Cleanup(func() { session.Close() })
	remote := session.(*sshGraphicSession)
	if err = remote.files.MkdirAll(GraphicWorkspace + "/submit/submit_step5"); err != nil {
		t.Fatal(err)
	}
	return session, remote
}

func writeWorkspaceFile(t *testing.T, remote *sshGraphicSession, relative string, content []byte) {
	t.Helper()
	f, err := remote.files.Create(GraphicWorkspace + "/" + relative)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write(content); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

const stepFile = "submit/submit_step5/step5.mdj"

// The production case: a student's step5.mdj that ReadFile refuses. Larger
// than the read limit, it is hashed in place and replaced by the answer.
func TestGraphicSaveReplacesAFileLargerThanTheReadLimit(t *testing.T) {
	session, remote := graphicSFTPFixture(t)
	big := []byte(strings.Repeat("x", MaxGraphicBytes+1))
	writeWorkspaceFile(t, remote, stepFile, big)
	if _, _, err := session.ReadFile(context.Background(), stepFile); !errors.Is(err, errGraphicIrregular) {
		t.Fatalf("large file read = %v", err)
	}
	hash, exists, err := remote.FileState(context.Background(), stepFile)
	if err != nil || !exists || hash != graphicHash(big) {
		t.Fatalf("state = %q %v %v", hash, exists, err)
	}
	if _, err = session.SaveAndCommit(context.Background(), stepFile, hash, true, []byte(graphicProject)); err != nil {
		t.Fatalf("save over large file: %v", err)
	}
	if got, exists, err := session.ReadFile(context.Background(), stepFile); err != nil || !exists || string(got) != graphicProject {
		t.Fatalf("answer not saved: %v %v", exists, err)
	}
}

// A link at the file is replaced as an entry; its target is never written.
func TestGraphicSaveReplacesALinkWithoutWritingItsTarget(t *testing.T) {
	session, remote := graphicSFTPFixture(t)
	writeWorkspaceFile(t, remote, "submit/elsewhere.mdj", []byte("elsewhere"))
	if err := remote.files.Symlink(GraphicWorkspace+"/submit/elsewhere.mdj", GraphicWorkspace+"/"+stepFile); err != nil {
		t.Fatal(err)
	}
	hash, exists, err := remote.FileState(context.Background(), stepFile)
	if err != nil || !exists || hash == graphicHash(nil) {
		t.Fatalf("link state = %q %v %v", hash, exists, err)
	}
	if _, err = session.SaveAndCommit(context.Background(), stepFile, hash, true, []byte(graphicProject)); err != nil {
		t.Fatalf("save over link: %v", err)
	}
	if got, _, err := session.ReadFile(context.Background(), "submit/elsewhere.mdj"); err != nil || string(got) != "elsewhere" {
		t.Fatalf("link target was written: %q %v", got, err)
	}
	if got, exists, err := session.ReadFile(context.Background(), stepFile); err != nil || !exists || string(got) != graphicProject {
		t.Fatalf("answer not saved over the link: %v %v", exists, err)
	}
}

// A directory where the file belongs is refused with that reason.
func TestGraphicSaveRefusesADirectoryAtTheFile(t *testing.T) {
	session, remote := graphicSFTPFixture(t)
	if err := remote.files.Mkdir(GraphicWorkspace + "/" + stepFile); err != nil {
		t.Fatal(err)
	}
	if _, _, err := remote.FileState(context.Background(), stepFile); err == nil || !strings.Contains(err.Error(), "目录或特殊文件") {
		t.Fatalf("directory state err = %v", err)
	}
	if _, err := session.SaveAndCommit(context.Background(), stepFile, graphicHash(nil), true, []byte(graphicProject)); err == nil {
		t.Fatal("saved over a directory")
	}
}
