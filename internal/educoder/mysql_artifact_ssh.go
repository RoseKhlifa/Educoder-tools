package educoder

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// mysqlWalk applies the same non-symlink workspace boundary as the StarUML
// transport, but accepts only an explicit result .txt path.
func (s *sshGraphicSession) mysqlWalk(relative string, create bool) error {
	if _, err := normalizeMySQLArtifactPath(relative); err != nil {
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
				return errors.New("MySQL 结果目录创建失败")
			}
			info, err = s.files.Lstat(current)
		}
		if os.IsNotExist(err) && !create {
			return os.ErrNotExist
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("MySQL 结果目录不是普通工作区目录")
		}
	}
	return nil
}

func (s *sshGraphicSession) ReadMySQLArtifact(ctx context.Context, relative string) ([]byte, bool, error) {
	stop := context.AfterFunc(ctx, func() { _ = s.client.Close() })
	defer stop()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if err := s.mysqlWalk(relative, false); err != nil {
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
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return nil, false, errors.New("MySQL 结果必须是大小受限的普通文件")
	}
	f, err := s.files.Open(target)
	if err != nil {
		return nil, false, errors.New("MySQL 结果读取失败")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if err != nil || len(data) > 64<<10 {
		return nil, false, errors.New("MySQL 结果过大或读取失败")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// mysqlRemoteLockCommand keeps an advisory flock held by the remote process.
// The lock survives a stale lock-file path but not a stale owner: closing the
// SSH channel releases the kernel lock even when the client timed out.
func mysqlRemoteLockCommand(lockPath string) string {
	q := graphicQuote(lockPath)
	return "set -eu; exec 9>" + q + "; flock -n 9 || { printf 'busy\\n'; exit 75; }; printf 'locked\\n'; trap 'exit 0' HUP INT TERM; trap 'flock -u 9' EXIT; while :; do sleep 3600; done"
}

type mysqlRemoteLock struct {
	session *ssh.Session
	done    chan error
	once    sync.Once
}

func (l *mysqlRemoteLock) release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		_ = l.session.Close()
		select {
		case <-l.done:
		case <-time.After(5 * time.Second):
		}
	})
}

func (s *sshGraphicSession) acquireMySQLLock(ctx context.Context, lockPath string) (*mysqlRemoteLock, error) {
	if s == nil || s.client == nil {
		return nil, errors.New("MySQL 锁缺少 SSH 会话")
	}
	session, err := s.client.NewSession()
	if err != nil {
		return nil, errors.New("MySQL 锁会话创建失败")
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		_ = session.Close()
		return nil, errors.New("MySQL 锁输出通道创建失败")
	}
	session.Stderr = io.Discard
	if err = session.Start(mysqlRemoteLockCommand(lockPath)); err != nil {
		_ = session.Close()
		return nil, errors.New("MySQL 锁启动失败")
	}
	lineCh := make(chan struct {
		line string
		err  error
	}, 1)
	go func() {
		line, readErr := bufio.NewReader(io.LimitReader(stdout, 128)).ReadString('\n')
		lineCh <- struct {
			line string
			err  error
		}{line: line, err: readErr}
	}()
	select {
	case <-ctx.Done():
		_ = session.Close()
		_ = session.Wait()
		return nil, ctx.Err()
	case result := <-lineCh:
		if result.err != nil || strings.TrimSpace(result.line) != "locked" {
			_ = session.Close()
			_ = session.Wait()
			if strings.TrimSpace(result.line) == "busy" {
				return nil, errors.New("MySQL 结果正在被另一提交处理")
			}
			return nil, errors.New("MySQL 锁未能取得")
		}
	}
	lock := &mysqlRemoteLock{session: session, done: make(chan error, 1)}
	go func() { lock.done <- session.Wait() }()
	return lock, nil
}

// mysqlArtifactCommand is generated exclusively from validated column objects.
// The password stays in this operation's in-memory SSH command; no command,
// credentials file, or raw script is persisted in the question bank/workspace.
// The random database is newly created without IF NOT EXISTS or DROP. Existing
// user databases and tables are never selected, overwritten, or removed.
func mysqlArtifactCommand(plan *MySQLArtifactPlan, username, password, db, temporary string) (string, error) {
	if plan == nil || plan.seal == "" || plan.seal != plan.planSeal() || plan.SchemaSHA256 != graphicHash([]byte(plan.CanonicalSQL())) || !mysqlIdentifier.MatchString(username) || !mysqlIdentifier.MatchString(db) || !strings.HasPrefix(db, "educoder_schema_") || len(password) == 0 || len(password) > 128 || strings.ContainsAny(password, "\x00\r\n") {
		return "", errors.New("MySQL 执行缺少密封的结构计划或临时数据库绑定")
	}
	if _, err := normalizeMySQLArtifactPath(temporary); err != nil {
		return "", err
	}
	create := "CREATE DATABASE `" + db + "` CHARACTER SET utf8;"
	execute := plan.CanonicalSQL() + " DESCRIBE `educoder_reference`;"
	command := "set -eu; set -C; cd " + graphicQuote(GraphicWorkspace) + "; "
	command += "educoder_mysql() { MYSQL_PWD=" + graphicQuote(password) + " mysql --connect-timeout=10 --protocol=TCP --host=127.0.0.1 --user=" + graphicQuote(username) + " \"$@\"; }; "
	command += "educoder_mysql --execute=" + graphicQuote(create) + "; "
	command += "educoder_mysql --database=" + graphicQuote(db) + " --table --execute=" + graphicQuote(execute) + " > " + graphicQuote(temporary)
	return command, nil
}

func (s *sshGraphicSession) ExecuteMySQLArtifact(ctx context.Context, plan *MySQLArtifactPlan, username, password string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { _ = s.client.Close() })
	defer stop()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if plan == nil || plan.seal == "" || plan.seal != plan.planSeal() {
		return "", errors.New("MySQL 执行计划已改变")
	}
	if err := s.mysqlWalk(plan.Path, true); err != nil {
		return "", err
	}
	before, existed, err := s.ReadMySQLArtifact(ctx, plan.Path)
	if err != nil {
		return "", err
	}
	beforeHash := graphicHash(before)
	lockPath := path.Join(GraphicWorkspace, ".educoder-mysql-"+graphicHash([]byte(plan.Path))+".lock")
	lock, err := s.acquireMySQLLock(ctx, lockPath)
	if err != nil {
		return "", err
	}
	defer lock.release()
	var random [16]byte
	if _, err = rand.Read(random[:]); err != nil {
		return "", errors.New("MySQL 独立数据库标识生成失败")
	}
	nonce := hex.EncodeToString(random[:])
	db := "educoder_schema_" + nonce
	temporary := path.Join(path.Dir(plan.Path), "educoder_mysql_"+nonce+".txt")
	command, err := mysqlArtifactCommand(plan, username, password, db, temporary)
	if err != nil {
		return "", err
	}
	defer s.files.Remove(path.Join(GraphicWorkspace, temporary))
	if _, err = s.command(ctx, command); err != nil {
		return "", errors.New("真实 MySQL 建表及 DESCRIBE 执行结果未确认")
	}
	generated, exists, err := s.ReadMySQLArtifact(ctx, temporary)
	if err != nil || !exists {
		return "", errors.New("MySQL 没有生成可读取的结果文件")
	}
	actual, err := ParseMySQLDescribe(string(generated))
	if err != nil || mysqlSchemaSQL(actual) != plan.CanonicalSQL() {
		return "", errors.New("MySQL 实际结果与引用表结构不一致")
	}
	current, exists, err := s.ReadMySQLArtifact(ctx, plan.Path)
	if err != nil {
		return "", err
	}
	if exists != existed || graphicHash(current) != beforeHash {
		return "", ErrGraphicChanged
	}
	target := path.Join(GraphicWorkspace, plan.Path)
	if err = s.files.PosixRename(path.Join(GraphicWorkspace, temporary), target); err != nil {
		return "", errors.New("MySQL 结果原子替换失败")
	}
	verify := func() error {
		current, exists, err := s.ReadMySQLArtifact(ctx, plan.Path)
		if err != nil || !exists || graphicHash(current) != graphicHash(generated) {
			return ErrGraphicChanged
		}
		return nil
	}
	if err = verify(); err != nil {
		return "", err
	}
	file := graphicQuote(plan.Path)
	commit, err := s.command(ctx, "cd "+graphicQuote(GraphicWorkspace)+" && git add -- "+file+" && if git diff --cached --quiet -- "+file+"; then git rev-parse HEAD; else git -c core.hooksPath=/dev/null -c user.email=service@educoder-tools.invalid -c user.name=educoder-tools commit --only -m 'Save MySQL task result' -- "+file+" >/dev/null && git rev-parse HEAD; fi")
	if err != nil {
		return "", err
	}
	if err = verify(); err != nil {
		return "", err
	}
	commit = strings.TrimSpace(commit)
	if !graphicCommit.MatchString(commit) {
		return "", errors.New("MySQL 结果缺少有效工作区提交版本")
	}
	return commit, nil
}
