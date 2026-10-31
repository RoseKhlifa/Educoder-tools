package educoder

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

var ErrHomeworkClosed = errors.New("homework is closed")

// homeworkLocation is the timezone used by the Educoder web application for
// date strings that do not carry an explicit offset. A fixed offset is
// intentional: Asia/Shanghai has no daylight-saving transitions, and this
// keeps availability decisions deterministic on hosts configured for UTC.
var homeworkLocation = time.FixedZone("Asia/Shanghai", 8*60*60)

// IsClosed reports whether an assignment should be excluded from the active
// homework list at now. An explicit upstream closed/ended status wins. When
// the status is absent or still active, the effective deadline is used. An
// assignment marked allow_late is retained unless Educoder supplies a valid
// late deadline, in which case that deadline is used instead.
//
// Missing or malformed dates are deliberately treated as open: a missing
// deadline is not evidence that a student has lost access.
func (h Homework) IsClosed(now time.Time) bool {
	if homeworkStatusClosed(h.Status) {
		return true
	}

	ordinary, ordinaryOK := firstHomeworkTime(h.Deadline, h.EndTime, h.EndDate)
	if h.AllowLate {
		if late, ok := firstHomeworkTime(h.LateEndTime, h.LateTime); ok {
			return !now.Before(late)
		}
		// Do not turn an allowed-late assignment into an expired one merely
		// because the optional extension field was omitted from the response.
		return false
	}
	return ordinaryOK && !now.Before(ordinary)
}

// OpenHomeworks is the user-service view. Protocol callers that need history,
// including verified collection, must continue using Homeworks directly.
func OpenHomeworks(homeworks []Homework, now time.Time) []Homework {
	result := make([]Homework, 0, len(homeworks))
	for _, homework := range homeworks {
		if !homework.IsClosed(now) {
			result = append(result, homework)
		}
	}
	return result
}

func homeworkStatusClosed(statuses []string) bool {
	for _, raw := range statuses {
		status := strings.ToLower(strings.TrimSpace(raw))
		switch status {
		case "已截止", "截止", "已结束", "结束", "closed", "ended", "expired", "over":
			return true
		}
	}
	return false
}

func firstHomeworkTime(values ...any) (time.Time, bool) {
	for _, value := range values {
		if parsed, ok := parseHomeworkTime(value); ok {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func parseHomeworkTime(value any) (time.Time, bool) {
	switch current := value.(type) {
	case nil:
		return time.Time{}, false
	case time.Time:
		return current, !current.IsZero()
	case json.Number:
		return parseHomeworkNumber(string(current))
	case float64:
		if math.IsNaN(current) || math.IsInf(current, 0) {
			return time.Time{}, false
		}
		return parseHomeworkNumber(strconv.FormatFloat(current, 'f', -1, 64))
	case float32:
		if math.IsNaN(float64(current)) || math.IsInf(float64(current), 0) {
			return time.Time{}, false
		}
		return parseHomeworkNumber(strconv.FormatFloat(float64(current), 'f', -1, 64))
	case int:
		return parseHomeworkNumber(strconv.Itoa(current))
	case int8:
		return parseHomeworkNumber(strconv.FormatInt(int64(current), 10))
	case int16:
		return parseHomeworkNumber(strconv.FormatInt(int64(current), 10))
	case int32:
		return parseHomeworkNumber(strconv.FormatInt(int64(current), 10))
	case int64:
		return parseHomeworkNumber(strconv.FormatInt(current, 10))
	case uint:
		return parseHomeworkNumber(strconv.FormatUint(uint64(current), 10))
	case uint8:
		return parseHomeworkNumber(strconv.FormatUint(uint64(current), 10))
	case uint16:
		return parseHomeworkNumber(strconv.FormatUint(uint64(current), 10))
	case uint32:
		return parseHomeworkNumber(strconv.FormatUint(uint64(current), 10))
	case uint64:
		return parseHomeworkNumber(strconv.FormatUint(current, 10))
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(current, &decoded) != nil {
			return time.Time{}, false
		}
		return parseHomeworkTime(decoded)
	case string:
		return parseHomeworkString(current)
	default:
		return time.Time{}, false
	}
}

func parseHomeworkString(raw string) (time.Time, bool) {
	value := strings.TrimSpace(raw)
	if value == "" || value == "-" || value == "--" || value == "null" {
		return time.Time{}, false
	}
	if numeric, err := strconv.ParseFloat(value, 64); err == nil {
		return parseHomeworkNumber(strconv.FormatFloat(numeric, 'f', -1, 64))
	}
	// First honor explicit offsets. Then interpret the upstream's timezone-less
	// forms in Asia/Shanghai, including both space and ISO-T separators.
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05Z07:00"} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed, true
		}
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02",
	} {
		if parsed, err := time.ParseInLocation(layout, value, homeworkLocation); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func parseHomeworkNumber(raw string) (time.Time, bool) {
	number, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) || number <= 0 {
		return time.Time{}, false
	}
	// Educoder uses Unix seconds in some responses and Unix milliseconds in
	// others. Values above this threshold are unambiguously milliseconds for
	// current dates. Preserve fractional seconds when seconds are supplied.
	if number > 100000000000 {
		number /= 1000
	}
	seconds, fraction := math.Modf(number)
	return time.Unix(int64(seconds), int64(fraction*1e9)).In(homeworkLocation), true
}
