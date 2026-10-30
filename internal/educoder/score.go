package educoder

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"regexp"
	"strings"
)

// Scores arrive as JSON numbers or decimal strings. Decode their original
// decimal text without a float64 conversion, preserving values below full score.
func (p *ChallengeProgress) UnmarshalJSON(data []byte) error {
	type progressAlias ChallengeProgress
	var decoded progressAlias
	wire := struct {
		*progressAlias
		ChallengeScore json.RawMessage `json:"challenge_score"`
		GameScore      json.RawMessage `json:"game_score"`
	}{progressAlias: &decoded}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	var err error
	decoded.ChallengeScore, err = scoreText(wire.ChallengeScore)
	if err != nil {
		return err
	}
	decoded.GameScore, err = scoreText(wire.GameScore)
	if err != nil {
		return err
	}
	*p = ChallengeProgress(decoded)
	return nil
}

func scoreText(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	if raw[0] == '"' {
		var text string
		err := json.Unmarshal(raw, &text)
		return text, err
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return "", errors.New("invalid score representation")
	}
	return number.String(), nil
}

var decimalScorePattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]{1,3})?$`)

func parseScore(text string) (*big.Rat, bool) {
	text = strings.TrimSpace(text)
	if len(text) > 128 || !decimalScorePattern.MatchString(text) {
		return nil, false
	}
	return new(big.Rat).SetString(text)
}

func scoreComparison(earned, maximum string) (known, full bool) {
	a, okA := parseScore(earned)
	b, okB := parseScore(maximum)
	if !okA || !okB || a.Sign() < 0 || b.Sign() <= 0 {
		return false, false
	}
	return true, a.Cmp(b) == 0
}

// HasFullScore is independent of submitted/passed labels, which do not prove
// that every objective question earned its available points.
func (p ChallengeProgress) HasFullScore() bool {
	_, full := scoreComparison(p.GameScore, p.ChallengeScore)
	return full
}
