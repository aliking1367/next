package tsin

import "strings"

// Problem codes say where a T.Sin failure comes from, so the dashboard and the
// Telegram alerts can name the place to look instead of repeating T.Sin's
// text. The dashboard translates each code; ExplainFA is the Telegram wording.
const (
	// ProblemInUse: T.Sin counts the token as online on another server. It
	// says so to every server but the one holding the session, and also, for
	// a while, to the same server after its tunnel died.
	ProblemInUse = "in_use"
	// ProblemDatacenterDown: the datacenter answers with a server error.
	ProblemDatacenterDown = "dc_down"
	// ProblemUnreachable: no connection to the datacenter at all, from the
	// server that asked.
	ProblemUnreachable = "unreachable"
	// ProblemTokenRefused: T.Sin answers but will not accept the token.
	ProblemTokenRefused = "token_refused"
	// ProblemUnknown is anything else; the raw text is shown with it.
	ProblemUnknown = "unknown"
)

// Classify names the kind of a T.Sin failure; "" for no error.
func Classify(err error) string {
	if err == nil {
		return ""
	}
	return ClassifyText(err.Error())
}

// ClassifyText does the same for an error that arrived as text, such as one
// a node reported.
func ClassifyText(text string) string {
	t := strings.ToLower(strings.TrimSpace(text))
	switch {
	case t == "":
		return ""
	case strings.Contains(t, "in use"):
		return ProblemInUse
	case strings.Contains(t, "server error"), strings.Contains(t, "http 5"), strings.Contains(t, "(http 5"),
		strings.Contains(t, "connection refused"), strings.Contains(t, "connection reset"):
		return ProblemDatacenterDown
	case strings.Contains(t, "unreachable"), strings.Contains(t, "timeout"), strings.Contains(t, "deadline"),
		strings.Contains(t, "no such host"), strings.Contains(t, "http 000"), strings.Contains(t, "network is"):
		return ProblemUnreachable
	case strings.Contains(t, "refused"), strings.Contains(t, "invalid"), strings.Contains(t, "expired"),
		strings.Contains(t, "not found"), strings.Contains(t, "http 4"):
		return ProblemTokenRefused
	}
	return ProblemUnknown
}

// ExplainFA says, in Persian, where the problem is and what to do about it.
func ExplainFA(code string) string {
	switch code {
	case ProblemInUse:
		return "T.Sin می‌گوید این توکن روی سرور دیگری آنلاین است. اگر تونل روی همین سرور تازه قطع شده، نگهبان جلسهٔ قبلی را می‌بندد و دوباره وصل می‌کند."
	case ProblemDatacenterDown:
		return "سرور دیتاسنتر T.Sin خطا می‌دهد؛ مشکل از طرف T.Sin است، نه پنل. اگر ادامه داشت، دیتاسنتر دیگری را امتحان کن یا به پشتیبانی T.Sin خبر بده."
	case ProblemUnreachable:
		return "این سرور اصلاً به دیتاسنتر T.Sin وصل نمی‌شود: یا دیتاسنتر کامل خاموش است، یا اینترنت یا فایروال همین سرور مشکل دارد."
	case ProblemTokenRefused:
		return "T.Sin توکن را قبول نمی‌کند: منقضی، غیرفعال یا اشتباه است. اکانت را در T.Sin چک کن."
	case ProblemUnknown:
		return "T.Sin جواب غیرمنتظره داد؛ متن خطا را ببین."
	}
	return ""
}

func prefixed(prefix, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return prefix + strings.TrimSpace(value)
}
