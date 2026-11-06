import {
	Box,
	Button,
	Code,
	HStack,
	IconButton,
	Progress,
	Stack,
	Text,
	Tooltip,
	useClipboard,
	useColorModeValue,
} from "@chakra-ui/react";
import {
	ArrowPathIcon,
	CheckIcon,
	ClipboardDocumentIcon,
	ExclamationTriangleIcon,
	XMarkIcon,
} from "@heroicons/react/24/outline";
import { type FC, useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { fetch } from "service/http";

type Step = {
	id: string;
	state: "ok" | "warn" | "fail" | "todo" | "skip";
	reason?: string;
	params?: Record<string, string | number>;
	values?: Record<string, string>;
	raw?: string;
};

type Checklist = { steps: Step[]; done: number; total: number };

type Translate = (
	key: string,
	fallback: string,
	options?: Record<string, unknown>,
) => string;

const CopyValue: FC<{ label: string; value: string }> = ({ label, value }) => {
	const { t } = useTranslation();
	const { onCopy, hasCopied } = useClipboard(value);
	return (
		<Box>
			<Text fontSize="xs" color="panel.textMuted" mb={0.5}>
				{label}
			</Text>
			<HStack spacing={1} align="stretch">
				<Code
					flex="1"
					minW={0}
					px={2}
					py={1}
					fontSize="xs"
					dir="ltr"
					whiteSpace="pre-wrap"
					wordBreak="break-all"
					borderRadius="md"
				>
					{value}
				</Code>
				<Tooltip
					label={
						hasCopied
							? t("tsin.copied", { defaultValue: "Copied" })
							: t("tsin.setup.copy", { defaultValue: "Copy" })
					}
					hasArrow
				>
					<IconButton
						aria-label={t("tsin.setup.copy", { defaultValue: "Copy" })}
						size="xs"
						h="auto"
						variant="outline"
						onClick={onCopy}
						icon={
							<Box
								as={hasCopied ? CheckIcon : ClipboardDocumentIcon}
								w="14px"
								h="14px"
							/>
						}
					/>
				</Tooltip>
			</HStack>
		</Box>
	);
};

// stepText is each step's title, a one-line summary, and for a step that
// needs doing, exactly what to do and the values to copy.
const stepText = (
	step: Step,
	tt: Translate,
): {
	title: string;
	summary: string;
	action?: string;
	values?: { label: string; value: string }[];
} => {
	const p = step.params ?? {};
	const v = step.values ?? {};
	const reason = step.reason ?? "";
	const skipped = tt(
		"tsin.setup.afterPrevious",
		"Checked after the steps above are done.",
	);
	switch (step.id) {
		case "token": {
			const title = tt("tsin.setup.token.title", "T.Sin token");
			if (step.state === "ok")
				return {
					title,
					summary:
						reason === "in_use_node" || reason === "via_node"
							? tt(
									"tsin.setup.token.okNode",
									"Accepted; the node holds the session",
								)
							: tt(
									"tsin.setup.token.ok",
									"Account {{client}} · {{countries}} countries",
									p,
								),
				};
			if (reason === "no_token")
				return {
					title,
					summary: tt("tsin.setup.todo", "Not done yet"),
					action: tt(
						"tsin.setup.token.noToken",
						"Buy a T.Sin subscription and copy its token. Paste it into the “T.Sin token” field below; it is saved when you press Apply.",
					),
				};
			if (reason === "inactive")
				return {
					title,
					summary: tt("tsin.inactive", "Inactive"),
					action: tt(
						"tsin.setup.token.inactive",
						"The T.Sin account is inactive or expired. Renew it at T.Sin; nothing needs changing here.",
					),
				};
			if (reason === "in_use")
				return {
					title,
					summary: tt("tsin.problem.inUse.where", "Token busy"),
					action: tt(
						"tsin.setup.token.inUse",
						"T.Sin says this token is online on another server. If you also use it in T.Sin's own app or on another server, stop it there. Otherwise wait a few minutes; the watch frees the old session by itself.",
					),
				};
			if (reason === "token_refused")
				return {
					title,
					summary: tt("tsin.problem.token.where", "Account"),
					action: tt(
						"tsin.setup.token.refused",
						"T.Sin does not accept this token: it is wrong, expired or disabled. Copy the token again from T.Sin, paste it below and press Apply.",
					),
				};
			return { title, summary: skipped };
		}
		case "datacenter": {
			const title = tt("tsin.setup.dc.title", "Datacenter");
			if (step.state === "ok")
				return {
					title,
					summary: p.latency_ms
						? tt(
								"tsin.setup.dc.okLatency",
								"{{dc}} answers · {{latency_ms}} ms",
								p,
							)
						: tt("tsin.setup.dc.ok", "{{dc}} answers", p),
				};
			if (reason === "dc_down")
				return {
					title,
					summary: tt("tsin.problem.dcDown.where", "T.Sin side"),
					action: tt(
						"tsin.setup.dc.down",
						"{{dc}} is failing on T.Sin's side; the panel is fine. Press “Find the best” below to pick a working datacenter, then press Apply.",
						p,
					),
				};
			if (reason === "unreachable")
				return {
					title,
					summary: tt("tsin.problem.unreachable.where", "No connection"),
					action: tt(
						"tsin.setup.dc.unreachable",
						"This server cannot reach {{dc}}. Press “Find the best” below: if other datacenters answer, {{dc}} is down; if none do, this server's internet or firewall blocks T.Sin (outgoing port 8080).",
						p,
					),
				};
			if (step.state === "fail")
				return {
					title,
					summary: tt("tsin.problem.unknown.where", "Error"),
					action: tt(
						"tsin.problem.unknown.title",
						"T.Sin answered unexpectedly",
					),
				};
			return { title, summary: skipped };
		}
		case "apply": {
			const title = tt(
				"tsin.setup.apply.title",
				"Choose and apply the countries",
			);
			if (step.state === "ok")
				return {
					title,
					summary: p.target
						? tt(
								"tsin.setup.apply.okNode",
								"{{count}} countries on {{target}}",
								p,
							)
						: tt(
								"tsin.setup.apply.ok",
								"{{count}} countries on this server",
								p,
							),
				};
			return {
				title,
				summary: tt("tsin.setup.todo", "Not done yet"),
				action: tt(
					"tsin.setup.apply.todo",
					"Below, choose where the countries run (this server or a node), the connection type and the countries, then press Apply. To serve every country on one domain on port 443 through Cloudflare, choose “One domain on port 443, by path”.",
				),
			};
		}
		case "cloudflare": {
			const title = tt("tsin.setup.cf.title", "Cloudflare for {{host}}", {
				host: p.host || "…",
			});
			const record = [
				{ label: tt("tsin.setup.v.type", "Type"), value: "A" },
				{ label: tt("tsin.setup.v.name", "Name"), value: v.record_name },
				...(v.record_value
					? [
							{
								label: tt("tsin.setup.v.ipv4", "IPv4 address"),
								value: v.record_value,
							},
						]
					: []),
				{
					label: tt("tsin.setup.v.proxy", "Proxy status"),
					value: "Proxied (orange cloud)",
				},
			];
			const skipRule = [
				{
					label: tt(
						"tsin.setup.v.skipRule",
						"Add at the end of your Skip rule's expression",
					),
					value: v.skip_rule,
				},
			];
			const originRule = [
				{
					label: tt(
						"tsin.setup.v.originRule",
						"Origin Rule: custom filter expression",
					),
					value: v.origin_rule,
				},
				{
					label: tt(
						"tsin.setup.v.originPort",
						"Origin Rule: Destination Port → Rewrite to",
					),
					value: v.origin_port,
				},
			];
			if (step.state === "ok")
				return {
					title,
					summary: tt(
						"tsin.setup.cf.ok",
						"Users reach the countries through Cloudflare on port 443",
					),
				};
			switch (reason) {
				case "no_host":
					return {
						title,
						summary: tt("tsin.setup.todo", "Not done yet"),
						action: tt(
							"tsin.setup.cf.noHost",
							"Enter the domain for the countries in “Cloudflare domain” below (for example g1.yourdomain.com), then press Apply. The domain must be on your Cloudflare account.",
						),
					};
				case "dns":
					return {
						title,
						summary: tt("tsin.setup.cf.dnsShort", "No DNS record"),
						action: tt(
							"tsin.setup.cf.dns",
							"In Cloudflare open your domain → DNS → Records → Add record, fill it in as below and Save. The orange cloud must be on.",
						),
						values: record,
					};
				case "not_proxied":
					return {
						title,
						summary: tt(
							"tsin.setup.cf.notProxiedShort",
							"Not through Cloudflare",
						),
						action: tt(
							"tsin.setup.cf.notProxied",
							"The domain does not go through Cloudflare. In Cloudflare → DNS → Records, edit the record of {{host}} and turn the cloud orange (Proxied), then Save.",
							p,
						),
						values: record,
					};
				case "challenge":
				case "waf_block":
					return {
						title,
						summary: tt(
							"tsin.setup.cf.blockedShort",
							"Blocked by a Cloudflare rule",
						),
						action: tt(
							"tsin.setup.cf.blocked",
							"A Cloudflare security rule stops the users' apps. In Cloudflare → Security → WAF → Custom rules, open your rule whose action is Skip (and is first in the list), press “Edit expression”, add the text below at the end, and Save.",
						),
						values: skipRule,
					};
				case "ssl_flexible":
					return {
						title,
						summary: tt("tsin.setup.cf.flexibleShort", "SSL mode is Flexible"),
						action: tt(
							"tsin.setup.cf.flexible",
							"In Cloudflare → SSL/TLS → Overview → Configure, choose “Full” (not “Full (strict)”) and Save.",
						),
					};
				case "ssl_strict":
					return {
						title,
						summary: tt(
							"tsin.setup.cf.strictShort",
							"SSL mode is Full (strict)",
						),
						action: tt(
							"tsin.setup.cf.strict",
							"“Full (strict)” refuses the gate's own certificate. In Cloudflare → SSL/TLS → Overview → Configure, choose “Full” and Save.",
						),
					};
				case "origin_down":
					return {
						title,
						summary: tt(
							"tsin.setup.cf.originShort",
							"Cloudflare cannot reach the server",
						),
						action: tt(
							"tsin.setup.cf.origin",
							"Cloudflare cannot reach the gate on port {{port}}. 1) In Cloudflare → Rules → Origin Rules → Create rule: “Custom filter expression” → Edit expression with the first value below; under Destination Port choose “Rewrite to” with the second value; Deploy. 2) Make sure the server's firewall allows port {{port}}. 3) Make sure the node is online.",
							p,
						),
						values: originRule,
					};
				case "no_path":
					return {
						title,
						summary: tt("tsin.problem.unknown.where", "Error"),
						action: tt(
							"tsin.setup.cf.noPath",
							"The country configs have no path yet. Choose “One domain on port 443, by path” below and press Apply again.",
						),
					};
				case "unreachable":
					return {
						title,
						summary: tt("tsin.problem.unreachable.where", "No connection"),
						action: tt(
							"tsin.setup.cf.unreachable",
							"This server could not open {{host}} on port 443. Check that the domain is on Cloudflare and the record below exists.",
							p,
						),
						values: record,
					};
				case "gate_answer":
					return {
						title,
						summary: tt(
							"tsin.setup.cf.gateShort",
							"Unexpected answer {{status}}",
							p,
						),
						action: tt(
							"tsin.setup.cf.gate",
							"Cloudflare reached the server but the gate answered {{status}} instead of connecting. Press Apply again; if it stays, check that the Origin Rule sends {{host}} to port {{port}}.",
							p,
						),
						values: originRule,
					};
			}
			return { title, summary: skipped };
		}
		case "direct": {
			const title = tt("tsin.setup.direct.title", "⚡ configs on {{address}}", {
				address:
					p.address || tt("tsin.setup.direct.serverIp", "the server's IP"),
			});
			if (step.state === "ok")
				return {
					title,
					summary:
						reason === "untested"
							? tt(
									"tsin.setup.direct.untested",
									"{{count}} configs; not tested",
									p,
								)
							: tt("tsin.setup.direct.ok", "{{count}} configs answer", p),
				};
			if (reason === "not_applied")
				return {
					title,
					summary: tt("tsin.setup.todo", "Not done yet"),
					action: tt(
						"tsin.setup.direct.notApplied",
						"The ⚡ countries are chosen but not made yet. Press Apply below.",
					),
				};
			if (reason === "closed")
				return {
					title,
					summary: tt(
						"tsin.setup.direct.closedShort",
						"No answer on port {{ports}}",
						p,
					),
					action: tt(
						"tsin.setup.direct.closed",
						"Nothing answers on {{address}} port {{ports}}. If {{address}} is an extra IP, it is probably not set up on the server yet: ask the provider how to attach it to this server, or add it to the server's network settings. Also make sure the server's firewall allows these ports. The Cloudflare configs are not affected.",
						p,
					),
				};
			return { title, summary: skipped };
		}
		case "node": {
			const title = tt(
				"tsin.setup.node.title",
				"Run the script on the node {{node}}",
				p,
			);
			const command = {
				label: tt("tsin.setup.v.command", "Then run this on the node"),
				value: "sudo /usr/local/sbin/next-tsin-report 0; echo",
			};
			if (step.state === "ok")
				return {
					title,
					summary: tt(
						"tsin.setup.node.ok",
						"Tunnel up · the node reported {{age}}s ago",
						p,
					),
				};
			switch (reason) {
				case "never":
					return {
						title,
						summary: tt("tsin.setup.todo", "Not done yet"),
						action: tt(
							"tsin.setup.node.never",
							"1) Press “Copy script” in “Run this on the node” below. 2) Log in to the node's server with SSH as root. 3) Paste the script and press Enter; when it asks, paste the T.Sin token (it is not shown while typing) and press Enter. 4) At the end you should see “Reporting to the panel: OK”. Then press “Check again” here.",
						),
					};
				case "report_blocked":
					return {
						title,
						summary: tt(
							"tsin.setup.node.blockedShort",
							"Cloudflare blocks the node's reports",
						),
						action: tt(
							"tsin.setup.node.blocked",
							"Cloudflare shows a robot check to the node, so its reports never reach the panel (the script prints “Reporting to the panel failed (HTTP 403)”). In Cloudflare → Security → WAF → Custom rules, open your Skip rule (the one for /api/), press “Edit expression”, add the first value below at the end and Save. If the script has not been run yet, run it first.",
						),
						values: [
							{
								label: tt(
									"tsin.setup.v.skipRule",
									"Add at the end of your Skip rule's expression",
								),
								value: v.report_skip_rule,
							},
							command,
						],
					};
				case "silent":
					return {
						title,
						summary: tt("tsin.card.nodeSilent", "Node not reporting"),
						action: tt(
							"tsin.setup.node.silent",
							"No report for {{minutes}} minutes: the node is off, its internet is down, or its watch stopped. Check that the node's server is on, then run the command below on it; it should print 200.",
							p,
						),
						values: [command],
					};
				case "tunnel_down":
					return {
						title,
						summary: tt("tsin.card.tunnelDown", "Tunnel down"),
						action: tt(
							"tsin.setup.node.down",
							"The tunnel on the node is down and its watch is repairing it by itself. If it stays down for more than 10 minutes, see the line below and the datacenter step above; it is usually T.Sin's side.",
						),
					};
			}
			return { title, summary: skipped };
		}
		case "tunnel": {
			const title = tt("tsin.setup.tunnel.title", "Tunnel on this server");
			if (step.state === "ok")
				return { title, summary: tt("tsin.tunnelUp", "Tunnel up") };
			if (reason === "down")
				return {
					title,
					summary: tt("tsin.card.tunnelDown", "Tunnel down"),
					action: tt(
						"tsin.setup.tunnel.down",
						"The tunnel on this server has no handshake. The watch is repairing it. If it stays down, this server's firewall may block outgoing UDP to T.Sin, or T.Sin's datacenter is down (see the datacenter step).",
					),
				};
			return { title, summary: skipped };
		}
		case "countries": {
			const title = tt("tsin.setup.countries.title", "Countries working");
			if (step.state === "ok")
				return {
					title,
					summary:
						reason === "panel_watch"
							? tt("tsin.setup.countries.panel", "Watched by the panel")
							: tt(
									"tsin.nodeReport.ports",
									"{{working}} of {{total}} countries answer",
									p,
								),
				};
			if (reason === "failing")
				return {
					title,
					summary: tt(
						"tsin.nodeReport.ports",
						"{{working}} of {{total}} countries answer",
						p,
					),
					action: tt(
						"tsin.setup.countries.failing",
						"These do not answer at T.Sin: {{failing}}. The problem is on T.Sin's side and the other countries work; nothing to change here.",
						p,
					),
				};
			if (reason === "no_test")
				return {
					title,
					summary: tt(
						"tsin.setup.countries.wait",
						"Tested by the node every 5 minutes once it reports",
					),
				};
			return { title, summary: skipped };
		}
		case "users": {
			const title = tt("tsin.setup.users.title", "Give it to users");
			if (step.state === "ok")
				return {
					title,
					summary: tt(
						"tsin.setup.users.ok",
						"{{count}} users have the service “{{service}}”",
						p,
					),
				};
			if (reason === "no_service")
				return {
					title,
					summary: tt("tsin.setup.todo", "Not done yet"),
					action: tt(
						"tsin.setup.users.noService",
						"The countries have no service yet. Press Apply below once more; it creates the service and puts every country in it.",
					),
				};
			if (reason === "no_users")
				return {
					title,
					summary: tt("tsin.setup.todo", "Not done yet"),
					action: tt(
						"tsin.setup.users.todo",
						"Users get the countries through the service “{{service}}”. When you create or edit a user (or in the Telegram bot), choose this service. Each country becomes a separate config in their subscription.",
						p,
					),
				};
			return { title, summary: skipped };
		}
	}
	return { title: step.id, summary: "" };
};

const stateStyle: Record<
	Step["state"],
	{ color: string; icon?: typeof CheckIcon }
> = {
	ok: { color: "green.400", icon: CheckIcon },
	warn: { color: "orange.400", icon: ExclamationTriangleIcon },
	fail: { color: "red.400", icon: XMarkIcon },
	todo: { color: "blue.300" },
	skip: { color: "gray.500" },
};

// TSinSetupChecklist walks the admin through setting up T.Sin. The panel
// checks every step itself; a step needing work says exactly what to do and
// gives the values to copy. reloadKey changes after an Apply, to check again.
export const TSinSetupChecklist: FC<{ reloadKey?: unknown }> = ({
	reloadKey,
}) => {
	const { t } = useTranslation();
	const tt: Translate = (key, fallback, options) =>
		t(key, { defaultValue: fallback, ...options }) as string;
	const [data, setData] = useState<Checklist | null>(null);
	const [loading, setLoading] = useState(false);
	const [failed, setFailed] = useState(false);
	const rowBg = useColorModeValue("blackAlpha.50", "whiteAlpha.50");

	const check = useCallback(async () => {
		setLoading(true);
		setFailed(false);
		try {
			setData(
				await fetch<Checklist>(
					`/core/tsin/checklist?origin=${encodeURIComponent(window.location.origin)}`,
				),
			);
		} catch {
			setFailed(true);
		} finally {
			setLoading(false);
		}
	}, []);

	// biome-ignore lint/correctness/useExhaustiveDependencies: reloadKey is the signal to check again
	useEffect(() => {
		void check();
	}, [check, reloadKey]);

	// The first step that needs work is opened; the rest stay one line.
	const current = data?.steps.findIndex(
		(step) => step.state !== "ok" && step.state !== "skip",
	);

	return (
		<Box borderWidth="1px" borderRadius="8px" p={3}>
			<HStack justify="space-between" mb={2} spacing={3}>
				<Box minW={0}>
					<Text fontWeight="semibold">
						{tt("tsin.setup.title", "Step-by-step setup")}
					</Text>
					{data && (
						<Text fontSize="xs" color="panel.textMuted">
							{data.done === data.total
								? tt("tsin.setup.allDone", "All done; everything works")
								: tt(
										"tsin.setup.progress",
										"{{done}} of {{total}} steps done",
										{
											done: data.done,
											total: data.total,
										},
									)}
						</Text>
					)}
				</Box>
				<Button
					size="xs"
					variant="outline"
					flexShrink={0}
					isLoading={loading}
					onClick={() => void check()}
					leftIcon={<Box as={ArrowPathIcon} w="14px" h="14px" />}
				>
					{tt("tsin.setup.recheck", "Check again")}
				</Button>
			</HStack>
			{data && (
				<Progress
					value={(data.done / Math.max(1, data.total)) * 100}
					size="xs"
					borderRadius="full"
					colorScheme={data.done === data.total ? "green" : "primary"}
					mb={3}
				/>
			)}
			{failed && (
				<Text fontSize="sm" color="red.400">
					{tt(
						"tsin.setup.failed",
						"Could not run the checks; press Check again.",
					)}
				</Text>
			)}
			{!data && loading && (
				<Text fontSize="sm" color="panel.textMuted">
					{tt("tsin.setup.checking", "Checking every step…")}
				</Text>
			)}
			<Stack spacing={1.5}>
				{data?.steps.map((step, index) => {
					const text = stepText(step, tt);
					const style = stateStyle[step.state];
					const open =
						Boolean(text.action) &&
						(index === current ||
							step.state === "fail" ||
							step.state === "warn");
					return (
						<Box
							key={step.id}
							borderRadius="6px"
							bg={open ? rowBg : undefined}
							px={2}
							py={1.5}
						>
							<HStack spacing={2.5} align="flex-start">
								<Box
									flexShrink={0}
									w="22px"
									h="22px"
									mt="1px"
									borderRadius="full"
									borderWidth="1.5px"
									borderColor={style.color}
									color={style.color}
									display="flex"
									alignItems="center"
									justifyContent="center"
									fontSize="xs"
									fontWeight="bold"
								>
									{style.icon ? (
										<Box as={style.icon} w="13px" h="13px" />
									) : (
										index + 1
									)}
								</Box>
								<Box minW={0} flex="1">
									<HStack
										justify="space-between"
										spacing={2}
										flexWrap="wrap"
										rowGap={0}
									>
										<Text
											fontSize="sm"
											fontWeight="semibold"
											color={
												step.state === "skip" ? "panel.textMuted" : undefined
											}
										>
											{text.title}
										</Text>
										<Text fontSize="xs" color={style.color}>
											{text.summary}
										</Text>
									</HStack>
									{open && (
										<Stack spacing={2} mt={1.5}>
											<Text fontSize="sm">{text.action}</Text>
											{text.values
												?.filter((item) => item.value)
												.map((item) => (
													<CopyValue
														key={item.label}
														label={item.label}
														value={item.value}
													/>
												))}
											{step.raw && (
												<Text
													fontSize="xs"
													color="panel.textMuted"
													dir="ltr"
													fontFamily="mono"
													wordBreak="break-all"
												>
													{step.raw}
												</Text>
											)}
										</Stack>
									)}
								</Box>
							</HStack>
						</Box>
					);
				})}
			</Stack>
		</Box>
	);
};
