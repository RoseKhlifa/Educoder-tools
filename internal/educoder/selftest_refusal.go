package educoder

import "strings"

// SelfTestRefusedError means log_output answered without a run stream and with
// the platform's own error text, so no code was executed. Resource pressure
// ("当前平台资源紧张") is transient; other refusals are reported as such. The
// platform text is never retained: only the fixed Kind is.
type SelfTestRefusedError struct {
	// Kind is "resource_busy" or "refused".
	Kind string
}

func (e *SelfTestRefusedError) Error() string {
	if e != nil && e.Kind == "resource_busy" {
		return "自测未启动：平台运行资源紧张"
	}
	return "自测未启动：平台拒绝启动运行环境"
}

// Transient reports whether waiting and retrying the same self-test can help.
func (e *SelfTestRefusedError) Transient() bool { return e != nil && e.Kind == "resource_busy" }

func selfTestRefusal(message string) error {
	if strings.Contains(message, "资源紧张") {
		return &SelfTestRefusedError{Kind: "resource_busy"}
	}
	return &SelfTestRefusedError{Kind: "refused"}
}

// EvaluationRefusedError means game_build answered with a refusal status the
// official client treats as "evaluation not started" (it stops without
// polling). The save before it happened; no formal evaluation did. Observed
// together with platform resource pressure, so it is transient.
type EvaluationRefusedError struct{ Code int }

func (e *EvaluationRefusedError) Error() string {
	return "正式评测未启动：平台拒绝了本次评测（通常为平台资源紧张）"
}
