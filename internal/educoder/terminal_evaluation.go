package educoder

import (
	"context"
	"errors"
)

// SubmitTerminal evaluates the workspace resulting from one confirmed terminal
// execution. The receipt is bound privately to the client and task and can be
// consumed once. It proves which script ran; it is not an editor-source digest.
func (c *Client) SubmitTerminal(ctx context.Context, t *Task, receipt *TerminalResult, options PollOptions) (*EvaluationResult, error) {
	env, err := terminalEnvironment(t)
	if err != nil {
		return nil, err
	}
	if receipt == nil || !receipt.Completed || receipt.ExitCode == nil || *receipt.ExitCode != 0 || receipt.ScriptHash == "" || receipt.state == nil || receipt.state.exitCode != 0 {
		return nil, errors.New("终端评测需要已确认成功执行的脚本回执")
	}
	b := receipt.binding
	if b.client != c || b.gameIdentifier != t.Game.Identifier || b.myshixunID != t.MyShixun.ID || b.gameID != t.Game.ID || b.challengeID != t.Challenge.ID || b.userID != t.User.UserID || b.environmentID != env.EnvironmentID || b.scriptHash != receipt.ScriptHash {
		return nil, errors.New("终端执行回执与当前任务不匹配")
	}
	current, err := c.LoadTask(ctx, t.CourseIdentifier, t.HomeworkID, t.Game.Identifier)
	if err != nil {
		return nil, err
	}
	if !sameTerminalTarget(t, current) {
		return nil, errors.New("终端任务身份或环境在评测前发生变化")
	}
	paths := current.CodePaths()
	if len(paths) != 1 {
		return nil, errors.New("终端任务必须具有唯一的上游占位文件")
	}
	original, err := c.ReadCodeFile(ctx, current, paths[0])
	if err != nil {
		return nil, err
	}
	if !receipt.state.consumed.CompareAndSwap(false, true) {
		return nil, errors.New("终端执行回执已用于评测，需先复核平台状态")
	}
	// Match the original tab_type=4 submit path, preserving its placeholder.
	// The generated solution is an execution script, never placeholder source.
	saved, err := c.saveCodeInEnvironment(ctx, current, paths[0], original.Content, true, env)
	if err != nil {
		return nil, err
	}
	readback, err := c.ReadCodeFile(ctx, current, paths[0])
	if err != nil || readback.Content != original.Content {
		return nil, errors.New("终端占位文件保存后发生变化，评测尚未启动")
	}
	if _, err := c.buildCodeInEnvironment(ctx, current, saved, env); err != nil {
		return nil, err
	}
	result, err := c.WaitEvaluation(ctx, current, saved, options)
	if err != nil || result == nil || !result.Passed {
		return result, err
	}
	check, err := c.LoadTask(ctx, t.CourseIdentifier, t.HomeworkID, t.Game.Identifier)
	if err != nil || !sameTerminalTarget(t, check) || check.Game.Status != 2 {
		return nil, errors.New("终端正式评测尚未在当前任务中确认通过")
	}
	progress, err := c.HomeworkProgress(ctx, t.CourseIdentifier, t.HomeworkID)
	if err != nil {
		return nil, err
	}
	for _, level := range progress.RequiredChallenges() {
		if level.ChallengeID == t.Challenge.ID && level.HasFullScore() {
			return result, nil
		}
	}
	return nil, errors.New("终端评测尚未取得当前关卡的课堂满分证据")
}

func sameTerminalTarget(before, after *Task) bool {
	if before == nil || after == nil {
		return false
	}
	a, ea := terminalEnvironment(before)
	b, eb := terminalEnvironment(after)
	return ea == nil && eb == nil && a.EnvironmentID == b.EnvironmentID &&
		before.CourseIdentifier == after.CourseIdentifier && before.HomeworkID == after.HomeworkID &&
		before.Game.Identifier == after.Game.Identifier && before.Game.ID == after.Game.ID &&
		before.MyShixun.ID == after.MyShixun.ID && before.MyShixun.Identifier == after.MyShixun.Identifier &&
		before.User.UserID == after.User.UserID && before.Challenge.ID == after.Challenge.ID &&
		before.Challenge.Path == after.Challenge.Path
}
