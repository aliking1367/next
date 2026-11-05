// T.Sin failures, named by where they come from so the admin knows where to
// look. The panel sends a code with the account error; for other errors, such
// as a refused Apply, the code is read from the text with the panel's rules
// (internal/app/tsin/problems.go).

export type TSinProblem =
	| "in_use"
	| "dc_down"
	| "unreachable"
	| "token_refused"
	| "unknown";

type Translate = (
	key: string,
	fallback: string,
	options?: Record<string, unknown>,
) => string;

export const classifyTSinError = (text?: string | null): TSinProblem | "" => {
	const t = (text ?? "").trim().toLowerCase();
	if (!t) return "";
	if (t.includes("in use")) return "in_use";
	if (
		t.includes("server error") ||
		t.includes("http 5") ||
		t.includes("connection refused") ||
		t.includes("connection reset")
	)
		return "dc_down";
	if (
		t.includes("unreachable") ||
		t.includes("timeout") ||
		t.includes("deadline") ||
		t.includes("no such host") ||
		t.includes("http 000") ||
		t.includes("network is")
	)
		return "unreachable";
	if (
		t.includes("refused") ||
		t.includes("invalid") ||
		t.includes("expired") ||
		t.includes("not found") ||
		t.includes("http 4")
	)
		return "token_refused";
	return "unknown";
};

export type TSinProblemText = {
	// where is a short label for a badge; title and hint explain it.
	where: string;
	title: string;
	hint: string;
};

// explainTSinProblem says where a problem is and what to do. onNode is
// whether the countries run on a node, which changes what "in use" means.
export const explainTSinProblem = (
	code: string,
	t: Translate,
	onNode = false,
): TSinProblemText => {
	switch (code) {
		case "in_use":
			return onNode
				? {
						where: t("tsin.problem.inUseNode.where", "On the node"),
						title: t(
							"tsin.problem.inUseNode.title",
							"T.Sin shows the account only to the node",
						),
						hint: t(
							"tsin.problem.inUseNode.hint",
							"The token is online on the node, so T.Sin will not answer the panel. This is normal. Run the latest node script on the node once so it reports here.",
						),
					}
				: {
						where: t("tsin.problem.inUse.where", "Token busy"),
						title: t(
							"tsin.problem.inUse.title",
							"T.Sin says the token is online on another server",
						),
						hint: t(
							"tsin.problem.inUse.hint",
							"Either the token is used on another server or app, or this server's tunnel just died and T.Sin still holds its old session. The watch ends that session and connects again by itself.",
						),
					};
		case "dc_down":
			return {
				where: t("tsin.problem.dcDown.where", "T.Sin side"),
				title: t(
					"tsin.problem.dcDown.title",
					"The T.Sin datacenter is failing",
				),
				hint: t(
					"tsin.problem.dcDown.hint",
					"T.Sin's server answers with an error. The problem is on T.Sin's side, not the panel. If it lasts, try Find the best for another datacenter or tell T.Sin support.",
				),
			};
		case "unreachable":
			return {
				where: t("tsin.problem.unreachable.where", "No connection"),
				title: t(
					"tsin.problem.unreachable.title",
					"No connection to the T.Sin datacenter",
				),
				hint: t(
					"tsin.problem.unreachable.hint",
					"Either the datacenter is fully down, or this server's internet or firewall blocks it. If other datacenters answer, it is T.Sin; if none do, check the server.",
				),
			};
		case "token_refused":
			return {
				where: t("tsin.problem.token.where", "Account"),
				title: t("tsin.problem.token.title", "T.Sin does not accept the token"),
				hint: t(
					"tsin.problem.token.hint",
					"The account is expired, disabled or the token is wrong. Check the account at T.Sin and enter the token again.",
				),
			};
		default:
			return {
				where: t("tsin.problem.unknown.where", "Error"),
				title: t("tsin.problem.unknown.title", "T.Sin answered unexpectedly"),
				hint: t(
					"tsin.problem.unknown.hint",
					"See the message below for the details.",
				),
			};
	}
};
