package educoder

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

var ErrGraphicChanged = errors.New("图形文件已被其他操作修改，请重新读取后再提交")

// GraphicSSHOptions accepts a deployment-provided host-key verifier. When nil,
// a transport pins the first observed key for each address for its lifetime.
// The same transport must be retained across reading and submitting a document.
type GraphicSSHOptions struct {
	HostKeyCallback ssh.HostKeyCallback
	Timeout         time.Duration
}
type SSHGraphicTransport struct {
	options  GraphicSSHOptions
	mu       sync.Mutex
	pins     map[string]string
	lookupIP func(context.Context, string) ([]net.IPAddr, error)
	dial     func(context.Context, string, string) (net.Conn, error)
}

func NewSSHGraphicTransport(options GraphicSSHOptions) *SSHGraphicTransport {
	return &SSHGraphicTransport{options: options, pins: map[string]string{}}
}

func (t *SSHGraphicTransport) hostKey(host string, addr net.Addr, key ssh.PublicKey) error {
	if t.options.HostKeyCallback != nil {
		return t.options.HostKeyCallback(host, addr, key)
	}
	fingerprint := ssh.FingerprintSHA256(key)
	t.mu.Lock()
	defer t.mu.Unlock()
	if old, ok := t.pins[host]; ok && old != fingerprint {
		return errors.New("图形环境 SSH 主机密钥发生变化")
	}
	t.pins[host] = fingerprint
	return nil
}

var graphicSSHUser = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)

func graphicPublicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4", "2001:db8::/32"} {
		_, block, _ := net.ParseCIDR(cidr)
		if block.Contains(ip) {
			return false
		}
	}
	return true
}
func (t *SSHGraphicTransport) Open(ctx context.Context, c GraphicConnection) (GraphicSession, error) {
	if !graphicSSHUser.MatchString(c.Username) || c.Password == "" || c.Port <= 0 || c.Port > 65535 || strings.ContainsAny(c.Host, "/\\@?#\r\n\x00") || strings.HasSuffix(strings.ToLower(c.Host), ".localhost") || strings.EqualFold(c.Host, "localhost") {
		return nil, errors.New("图形环境连接参数无效")
	}
	timeout := t.options.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	lookup := t.lookupIP
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	ips, err := lookup(ctx, c.Host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("图形环境地址解析失败")
	}
	for _, ip := range ips {
		if !graphicPublicIP(ip.IP) {
			return nil, errors.New("图形环境必须使用公网地址")
		}
	}
	address := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	var raw net.Conn
	dial := t.dial
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	for {
		for _, ip := range ips {
			attempt, cancelAttempt := context.WithTimeout(ctx, 10*time.Second)
			raw, err = dial(attempt, "tcp", net.JoinHostPort(ip.IP.String(), strconv.Itoa(c.Port)))
			cancelAttempt()
			if err == nil {
				break
			}
		}
		if err == nil {
			break
		}
		// Starting a graphical environment often precedes its SSH listener.
		// Only connection establishment is retried; no write or build is replayed.
		timer := time.NewTimer(2 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, errors.New("图形环境网络连接等待结束")
		case <-timer.C:
		}
	}
	if err != nil {
		return nil, errors.New("图形环境网络连接失败")
	}
	deadline, _ := ctx.Deadline()
	_ = raw.SetDeadline(deadline)
	config := &ssh.ClientConfig{User: c.Username, Auth: []ssh.AuthMethod{ssh.Password(c.Password)}, HostKeyCallback: t.hostKey, Timeout: timeout}
	connection, channels, requests, err := ssh.NewClientConn(raw, address, config)
	if err != nil {
		raw.Close()
		return nil, errors.New("图形环境 SSH 握手失败")
	}
	_ = raw.SetDeadline(time.Time{})
	client := ssh.NewClient(connection, channels, requests)
	files, err := sftp.NewClient(client)
	if err != nil {
		client.Close()
		return nil, errors.New("图形环境 SFTP 启动失败")
	}
	return &sshGraphicSession{client: client, files: files}, nil
}

type sshGraphicSession struct {
	client *ssh.Client
	files  *sftp.Client
}

func (s *sshGraphicSession) Close() error { _ = s.files.Close(); return s.client.Close() }
func graphicQuote(value string) string    { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

// walk never follows a symlink, including the workspace itself. Missing
// directories are created component by component only for a validated target.
func (s *sshGraphicSession) walk(relative string, create bool) error {
	if _, err := normalizeGraphicPath(relative); err != nil {
		return err
	}
	parts := strings.Split(strings.TrimPrefix(GraphicWorkspace, "/"), "/")
	parts = append(parts, strings.Split(path.Dir(relative), "/")...)
	current := "/"
	for _, part := range parts {
		if part == "." {
			continue
		}
		current = path.Join(current, part)
		info, err := s.files.Lstat(current)
		if os.IsNotExist(err) && create && strings.HasPrefix(current, GraphicWorkspace+"/") {
			if err = s.files.Mkdir(current); err != nil && !os.IsExist(err) {
				return errors.New("图形目标目录创建失败")
			}
			info, err = s.files.Lstat(current)
		}
		if os.IsNotExist(err) && !create {
			return os.ErrNotExist
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("图形目标目录不是普通工作区目录")
		}
	}
	return nil
}
func (s *sshGraphicSession) ReadFile(ctx context.Context, relative string) ([]byte, bool, error) {
	stop := context.AfterFunc(ctx, func() { _ = s.client.Close() })
	defer stop()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := s.walk(relative, false); err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	target := path.Join(GraphicWorkspace, relative)
	info, err := s.files.Lstat(target)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, errors.New("图形文件元信息读取失败")
	}
	if !info.Mode().IsRegular() || info.Size() > MaxGraphicBytes {
		return nil, false, errors.New("图形文件必须是大小受限的普通文件")
	}
	f, err := s.files.Open(target)
	if err != nil {
		return nil, false, errors.New("图形文件打开失败")
	}
	defer f.Close()
	content, err := io.ReadAll(io.LimitReader(f, MaxGraphicBytes+1))
	if err != nil || len(content) > MaxGraphicBytes {
		return nil, false, errors.New("图形文件读取失败或超过大小上限")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return content, true, nil
}

type graphicBoundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *graphicBoundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len() < b.limit {
		keep := b.limit - b.Len()
		if keep > len(p) {
			keep = len(p)
		}
		_, _ = b.Buffer.Write(p[:keep])
	}
	return n, nil
}
func (s *sshGraphicSession) command(ctx context.Context, command string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	session, err := s.client.NewSession()
	if err != nil {
		return "", errors.New("图形环境命令会话创建失败")
	}
	defer session.Close()
	var out graphicBoundedBuffer
	out.limit = 8192
	session.Stdout = &out
	session.Stderr = io.Discard
	if err = session.Start(command); err != nil {
		return "", errors.New("图形环境命令启动失败")
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return "", ctx.Err()
	case err = <-done:
		if err != nil {
			return "", errors.New("图形环境命令执行失败")
		}
	}
	return out.String(), nil
}
func (s *sshGraphicSession) SaveAndCommit(ctx context.Context, relative, expectedHash string, expectedExists bool, content []byte) (string, error) {
	stop := context.AfterFunc(ctx, func() { _ = s.client.Close() })
	defer stop()
	if _, err := normalizeGraphicPath(relative); err != nil {
		return "", err
	}
	if err := ValidateGraphicContent(string(content)); err != nil {
		return "", err
	}
	if len(expectedHash) != 64 {
		return "", errors.New("图形文件缺少原始版本")
	}
	lock := ".educoder-graphic-" + graphicHash([]byte(relative)) + ".lock"
	if _, err := s.command(ctx, "cd "+graphicQuote(GraphicWorkspace)+" && mkdir -- "+graphicQuote(lock)); err != nil {
		return "", errors.New("图形文件正在被另一提交处理")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.command(cleanup, "cd "+graphicQuote(GraphicWorkspace)+" && rmdir -- "+graphicQuote(lock))
	}()
	check := func() error {
		old, exists, err := s.ReadFile(ctx, relative)
		if err != nil {
			return err
		}
		if exists != expectedExists || graphicHash(old) != expectedHash {
			return ErrGraphicChanged
		}
		return nil
	}
	if err := check(); err != nil {
		return "", err
	}
	if err := s.walk(relative, true); err != nil {
		return "", err
	}
	var suffix [12]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", errors.New("图形临时文件标识生成失败")
	}
	target := path.Join(GraphicWorkspace, relative)
	temporary := target + ".educoder-" + hex.EncodeToString(suffix[:]) + ".tmp"
	f, err := s.files.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return "", errors.New("图形临时文件创建失败")
	}
	defer s.files.Remove(temporary)
	_, writeErr := f.Write(content)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		return "", errors.New("图形内容写入失败")
	}
	if err := check(); err != nil {
		return "", err
	}
	if err := s.files.PosixRename(temporary, target); err != nil {
		return "", errors.New("图形文件原子替换失败")
	}
	verify := func() error {
		current, exists, err := s.ReadFile(ctx, relative)
		if err != nil {
			return err
		}
		if !exists || graphicHash(current) != graphicHash(content) {
			return ErrGraphicChanged
		}
		return nil
	}
	if err := verify(); err != nil {
		return "", err
	}
	// --only and the exact validated path keep unrelated staged files out of
	// this commit. Hooks are disabled; repository-controlled hooks never run.
	file := graphicQuote(relative)
	command := "cd " + graphicQuote(GraphicWorkspace) + " && git add -- " + file + " && if git diff --cached --quiet -- " + file + "; then git rev-parse HEAD; else git -c core.hooksPath=/dev/null -c user.email=service@educoder-tools.invalid -c user.name=educoder-tools commit --only -m 'Save graphic task' -- " + file + " >/dev/null && git rev-parse HEAD; fi"
	commit, err := s.command(ctx, command)
	if err != nil {
		return "", err
	}
	if err := verify(); err != nil {
		return "", err
	}
	commit = strings.TrimSpace(commit)
	if !graphicCommit.MatchString(commit) {
		return "", errors.New("图形 Git 提交版本无效")
	}
	return commit, nil
}
