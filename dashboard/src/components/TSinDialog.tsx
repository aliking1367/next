import {
	Alert,
	AlertDescription,
	AlertIcon,
	Badge,
	Box,
	Button,
	Checkbox,
	FormControl,
	FormHelperText,
	FormLabel,
	HStack,
	Progress,
	SimpleGrid,
	Stack,
	Switch,
	Text,
	Textarea,
	useClipboard,
	useToast,
} from "@chakra-ui/react";
import { PanelSelect as Select } from "components/common/PanelSelect";
import { AppDialog } from "components/dialogs/AppDialog";
import { ConfirmDialog } from "components/dialogs/ConfirmDialog";
import { Input } from "components/Input";
import { type FC, useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { fetch } from "service/http";
import { TSinSetupChecklist } from "./TSinSetupChecklist";
import { classifyTSinError, explainTSinProblem } from "./tsinProblems";

type TSinProxy = {
	port: number;
	country: string;
	mode: string;
	ping_ms: string;
};

type TSinAccount = {
	client_name: string;
	data_used_gb: number;
	data_limit_gb: number;
	active_connections: number;
	expiry_date: string;
	active: boolean;
	proxies: TSinProxy[];
};

type TSinTarget = {
	id: string;
	type: string;
	name: string;
	status?: string;
};

type TSinView = {
	target: string;
	transport?: string;
	gate_host?: string;
	gate_port?: number;
	// direct_countries also get a config on the server's own IP in path mode;
	// null until an admin chooses.
	direct_countries?: string[] | null;
	direct_address?: string;
	emergency?: TSinEmergency;
	targets?: TSinTarget[];
	node_script: string;
	dc: string;
	datacenters: string[];
	preferred: string[];
	token_set: boolean;
	countries: string[] | null;
	applied: boolean;
	last_applied_at?: string;
	last_detail?: string;
	service_name: string;
	tunnel: { up: boolean; handshake?: string };
	account?: TSinAccount;
	account_error?: string;
	account_error_code?: string;
	// node_report is what the node running the tunnel last reported.
	node_report?: {
		age_seconds: number;
		fresh: boolean;
		dc: string;
		tunnel_up: boolean;
		handshake_age: number;
		log?: string[] | null;
		ports_total?: number;
		ports_working?: number;
		ports_failing?: string[] | null;
	};
};

// TSinEmergency is the emergency tunnel for when T.Sin is down: an Iranian
// tunnel forwarding to the countries' server, whose configs go out from that
// server directly.
type TSinEmergency = {
	address: string;
	vmess_port: number;
	ss_port: number;
	enabled: boolean;
	node_address?: string;
	node_ports?: { vmess?: number; shadowsocks?: number };
};

type TSinDatacenterRank = {
	dc: string;
	ok: boolean;
	latency_ms: number;
	countries?: number;
	error?: string;
	problem?: string;
};

type TSinApplyResponse = {
	needs_node_script?: boolean;
	service_name: string;
	tunnel_up: boolean;
	warning?: string;
	inbounds: { tag: string; port: number; country: string }[];
};

const formatGB = (value: number) => `${value.toFixed(2)} GB`;

// DIRECT_SUGGESTED are the countries offered a direct config until the admin
// chooses: the ones most users pick.
const DIRECT_SUGGESTED = [
	"Best ping",
	"Germany",
	"The Netherlands",
	"Turkey",
	"United Kingdom",
	"United States",
];

const expiryText = (
	value: string,
	t: (
		key: string,
		fallback: string,
		options?: Record<string, unknown>,
	) => string,
) => {
	const when = new Date(value.replace(" ", "T"));
	if (Number.isNaN(when.getTime())) return value;
	const days = Math.ceil((when.getTime() - Date.now()) / 86_400_000);
	if (days < 0) return t("tsin.expired", "Expired");
	if (days === 0) return t("tsin.expiresToday", "Expires today");
	return t("tsin.daysLeft", "{{count}} day(s) left", { count: days });
};

export const TSinDialog: FC<{
	isOpen: boolean;
	onClose: () => void;
	onChanged?: () => void;
}> = ({ isOpen, onClose, onChanged }) => {
	const { t } = useTranslation();
	const toast = useToast();
	const tt = (
		key: string,
		fallback: string,
		options?: Record<string, unknown>,
	) => t(key, { defaultValue: fallback, ...options }) as string;

	const [view, setView] = useState<TSinView | null>(null);
	const [loading, setLoading] = useState(false);
	const [busy, setBusy] = useState(false);
	const [token, setToken] = useState("");
	const [dc, setDc] = useState("dc1");
	const [target, setTarget] = useState("master");
	const [transport, setTransport] = useState("xhttp");
	const [gateHost, setGateHost] = useState("");
	const [gatePort, setGatePort] = useState("2087");
	const [selected, setSelected] = useState<string[]>([]);
	const [direct, setDirect] = useState<string[]>([]);
	const [directAddress, setDirectAddress] = useState("");
	const [emAddress, setEmAddress] = useState("");
	const [emVMessPort, setEmVMessPort] = useState("");
	const [emSSPort, setEmSSPort] = useState("");
	const [emBusy, setEmBusy] = useState(false);
	const [result, setResult] = useState<TSinApplyResponse | null>(null);
	const [confirmRemove, setConfirmRemove] = useState(false);
	const [ranking, setRanking] = useState(false);
	const [ranked, setRanked] = useState<TSinDatacenterRank[] | null>(null);
	// The node reports to the panel at the address the admin reaches it at.
	const nodeScript = (view?.node_script ?? "")
		.split("@PANEL_URL@")
		.join(window.location.origin);
	const script = useClipboard(nodeScript);

	// providerError turns a refused request into where the problem is and
	// what to do, keeping T.Sin's own words underneath.
	const providerError = (detail: string | undefined, fallback: string) => {
		const code = classifyTSinError(detail);
		if (!detail || !code) {
			toast({ status: "error", isClosable: true, title: detail || fallback });
			return;
		}
		const explained = explainTSinProblem(code, tt, target.startsWith("node:"));
		toast({
			status: "error",
			isClosable: true,
			duration: 12_000,
			title: explained.title,
			description: `${explained.hint} (${detail})`,
		});
	};

	// Asks every datacenter at once with the typed token, or the saved one,
	// and selects the fastest that accepts it.
	// Countries served from a node use the tunnel the node script made on that
	// node's datacenter. Applying another datacenter here would point their
	// configs at ports that tunnel does not have, so it is never switched for
	// them silently.
	const nodeTunnel = Boolean(view?.applied) && target.startsWith("node:");
	const dcChangedOnNode = nodeTunnel && dc !== view?.dc;

	const findBestDatacenter = async () => {
		setRanking(true);
		try {
			const data = await fetch<{
				datacenters: TSinDatacenterRank[];
				best: string;
			}>("/core/tsin/datacenters", {
				method: "POST",
				body: token.trim() ? { token: token.trim() } : {},
			});
			setRanked(data.datacenters);
			if (data.best && nodeTunnel) {
				// Shown, not chosen: see nodeTunnel.
			} else if (data.best) {
				setDc(data.best);
			} else if (
				!(
					nodeTunnel &&
					data.datacenters.some((item) => item.problem === "in_use")
				)
			) {
				// With the tunnel on a node, T.Sin answers "in use" to the panel;
				// the list says so per datacenter, which is not "none accepts it".
				toast({
					status: "warning",
					title: tt(
						"tsin.noDatacenter",
						"No datacenter accepts this token right now.",
					),
				});
			}
		} catch (error) {
			const detail = (error as { data?: { detail?: string } })?.data?.detail;
			providerError(detail, String(error));
		} finally {
			setRanking(false);
		}
	};

	// biome-ignore lint/correctness/useExhaustiveDependencies: the dialog loads once each time it opens, not whenever the toast or translator changes identity
	const load = useCallback(async () => {
		setLoading(true);
		try {
			const data = await fetch<TSinView>("/core/tsin");
			setView(data);
			setDc(data.dc);
			setTarget(data.target || "master");
			// The stored choice wins whenever there is one: falling back to XHTTP
			// just because the last apply failed rebuilt every country on the
			// next Apply and silently dropped the path-mode gate.
			setTransport(
				data.applied || data.gate_host ? data.transport || "tcp" : "xhttp",
			);
			setGateHost(data.gate_host ?? "");
			setGatePort(String(data.gate_port || 2087));
			// A country the account no longer offers would stay selected with no
			// checkbox to clear it, and the server refuses the whole apply.
			const offered = new Set(
				(data.account?.proxies ?? []).map((proxy) => proxy.country),
			);
			const wanted = data.countries?.length
				? data.countries
				: (data.preferred ?? []);
			// Never chosen yet: suggest the countries most users pick, so the
			// subscription gains only a few direct configs.
			setDirect(data.direct_countries ?? DIRECT_SUGGESTED);
			setDirectAddress(data.direct_address ?? "");
			setEmAddress(data.emergency?.address ?? "");
			setEmVMessPort(
				data.emergency?.vmess_port ? String(data.emergency.vmess_port) : "",
			);
			setEmSSPort(
				data.emergency?.ss_port ? String(data.emergency.ss_port) : "",
			);
			setSelected(
				offered.size ? wanted.filter((name) => offered.has(name)) : wanted,
			);
			setToken("");
		} catch (error: any) {
			toast({
				status: "error",
				isClosable: true,
				title:
					error?.data?.detail ?? tt("tsin.loadFailed", "Could not load T.Sin"),
			});
		} finally {
			setLoading(false);
		}
	}, []);

	useEffect(() => {
		if (isOpen) {
			setResult(null);
			void load();
		}
	}, [isOpen, load]);

	const available = view?.account?.proxies ?? [];
	const names = available.length
		? available.map((proxy) => proxy.country)
		: (view?.preferred ?? []);
	const toggle = (name: string) =>
		setSelected((current) =>
			current.includes(name)
				? current.filter((item) => item !== name)
				: [...current, name],
		);

	const apply = async () => {
		if (busy) return;
		setBusy(true);
		try {
			const body: Record<string, unknown> = {
				dc,
				target,
				transport,
				gate_host: gateHost.trim(),
				gate_port: Number.parseInt(gatePort, 10) || 2087,
				direct_countries: direct.filter((name) => selected.includes(name)),
				direct_address: directAddress.trim(),
				countries: selected,
			};
			if (token.trim()) body.token = token.trim();
			const response = await fetch<TSinApplyResponse>("/core/tsin/apply", {
				method: "POST",
				body,
			});
			setResult(response);
			toast({
				status: response.warning ? "warning" : "success",
				isClosable: true,
				title:
					response.warning ?? tt("tsin.applied", "T.Sin countries are ready"),
			});
			onChanged?.();
			await load();
		} catch (error: any) {
			providerError(
				error?.data?.detail,
				tt("tsin.failed", "Could not apply T.Sin"),
			);
		} finally {
			setBusy(false);
		}
	};

	// saveEmergency stores the emergency tunnel and, with enabled, shows or
	// hides its configs for every user of the countries.
	const saveEmergency = async (enabled?: boolean) => {
		if (emBusy) return;
		setEmBusy(true);
		try {
			const body: Record<string, unknown> = {
				address: emAddress.trim(),
				vmess_port: Number.parseInt(emVMessPort, 10) || 0,
				ss_port: Number.parseInt(emSSPort, 10) || 0,
			};
			if (enabled !== undefined) body.enabled = enabled;
			const result = await fetch<TSinEmergency>("/core/tsin/emergency", {
				method: "POST",
				body,
			});
			setView((current) =>
				current ? { ...current, emergency: result } : current,
			);
			toast({
				status: result.enabled ? "warning" : "success",
				isClosable: true,
				duration: 10_000,
				title: result.enabled
					? tt(
							"tsin.emergency.turnedOn",
							"Emergency tunnel is on. Users must update their subscription to get the 🆘 configs.",
						)
					: enabled === false
						? tt(
								"tsin.emergency.turnedOff",
								"Emergency tunnel is off. The 🆘 configs disappear when users update their subscription.",
							)
						: tt("tsin.emergency.saved", "Emergency tunnel saved"),
			});
		} catch (error: any) {
			toast({
				status: "error",
				isClosable: true,
				title: error?.data?.detail ?? String(error),
			});
		} finally {
			setEmBusy(false);
		}
	};

	const remove = async () => {
		setConfirmRemove(false);
		setBusy(true);
		try {
			await fetch("/core/tsin", { method: "DELETE" });
			toast({
				status: "success",
				isClosable: true,
				title: tt("tsin.removed", "T.Sin removed"),
			});
			onChanged?.();
			await load();
		} catch (error: any) {
			toast({
				status: "error",
				isClosable: true,
				title:
					error?.data?.detail ??
					tt("tsin.removeFailed", "Could not remove T.Sin"),
			});
		} finally {
			setBusy(false);
		}
	};

	const account = view?.account;
	const usedPercent =
		account && account.data_limit_gb > 0
			? Math.min(100, (account.data_used_gb / account.data_limit_gb) * 100)
			: 0;

	return (
		<>
			<AppDialog
				isOpen={isOpen}
				onClose={onClose}
				size="xl"
				title="T.Sin"
				overlayProps={{ bg: "blackAlpha.300" }}
				footer={
					<>
						{view?.applied && (
							<Button
								variant="ghost"
								colorScheme="red"
								mr="auto"
								isDisabled={busy}
								onClick={() => setConfirmRemove(true)}
							>
								{tt("tsin.remove", "Remove T.Sin")}
							</Button>
						)}
						<Button variant="ghost" onClick={onClose}>
							{tt("close", "Close")}
						</Button>
						<Button
							colorScheme="primary"
							isLoading={busy}
							isDisabled={loading || selected.length === 0}
							onClick={apply}
						>
							{tt("tsin.apply", "Apply")}
						</Button>
					</>
				}
			>
				<Stack spacing={4}>
					<Text fontSize="sm">
						{tt(
							"tsin.intro",
							"Sends each country's config out through that country's T.Sin exit, so users see an address from that country. Each chosen country becomes its own config in the service below.",
						)}
					</Text>

					{isOpen && <TSinSetupChecklist reloadKey={view} />}

					<Alert status="info" borderRadius="6px" fontSize="sm">
						<AlertIcon />
						<AlertDescription>
							{tt(
								"tsin.scope",
								"The T.Sin tunnel lives on one server, and the countries are served by the Xray on that server. Pick the panel server, or a node you run the traffic on.",
							)}
						</AlertDescription>
					</Alert>

					<FormControl>
						<FormLabel>
							{tt("tsin.target", "Serve the countries from")}
						</FormLabel>
						<Select
							value={target}
							onChange={(event) => setTarget(event.target.value)}
						>
							<option value="master">
								{tt("tsin.targetMaster", "This server (the panel)")}
							</option>
							{(view?.targets ?? [])
								.filter((item) => item.type === "node")
								.map((item) => (
									<option key={item.id} value={item.id}>
										{item.name} {item.status ? `(${item.status})` : ""}
									</option>
								))}
						</Select>
						{target !== "master" && (
							<FormHelperText>
								{tt(
									"tsin.targetNodeHelp",
									"The node switches to its own Xray config (a copy of the shared one made now). Later changes to the shared config will not reach it.",
								)}
							</FormHelperText>
						)}
					</FormControl>

					<FormControl>
						<FormLabel>{tt("tsin.transport", "Connection type")}</FormLabel>
						<Select
							value={transport}
							onChange={(event) => setTransport(event.target.value)}
						>
							<option value="xhttp">
								{tt("tsin.transportXhttp", "XHTTP (recommended)")}
							</option>
							<option value="tcp">{tt("tsin.transportTcp", "TCP")}</option>
							<option value="path">
								{tt(
									"tsin.transportPath",
									"One domain on port 443, by path (Cloudflare)",
								)}
							</option>
						</Select>
						<FormHelperText>
							{tt(
								"tsin.transportHelp",
								"XHTTP held up on Iranian networks where TCP connections kept being cut every few minutes. Changing this rebuilds the country configs on new ports, so users must update their subscription.",
							)}
						</FormHelperText>
					</FormControl>

					{transport === "path" && (
						<Box borderWidth="1px" borderRadius="6px" p={3}>
							<SimpleGrid columns={{ base: 1, sm: 2 }} spacing={3}>
								<FormControl>
									<FormLabel>
										{tt("tsin.gateHost", "Cloudflare domain")}
									</FormLabel>
									<Input
										value={gateHost}
										onChange={(event) => setGateHost(event.target.value)}
										placeholder="g1.example.com"
										dir="ltr"
										autoComplete="off"
										spellCheck={false}
									/>
								</FormControl>
								<FormControl>
									<FormLabel>{tt("tsin.gatePort", "Origin port")}</FormLabel>
									<Input
										value={gatePort}
										onChange={(event) => setGatePort(event.target.value)}
										placeholder="2087"
										dir="ltr"
										inputMode="numeric"
									/>
								</FormControl>
							</SimpleGrid>
							<Text fontSize="xs" opacity={0.8} mt={2}>
								{tt(
									"tsin.gateHelp",
									"In Cloudflare: an A record for this domain pointing at the node's IP, proxied (orange). SSL/TLS mode Full. An Origin Rule for this hostname that rewrites the destination port to the origin port above. Every country is then this domain on port 443, told apart by path.",
								)}
							</Text>
						</Box>
					)}

					<FormControl>
						<FormLabel>{tt("tsin.token", "T.Sin token")}</FormLabel>
						<Input
							type="password"
							value={token}
							onChange={(event) => setToken(event.target.value)}
							placeholder={
								view?.token_set
									? tt(
											"tsin.tokenStored",
											"A token is saved. Leave empty to keep it.",
										)
									: "token"
							}
							dir="ltr"
							autoComplete="off"
							spellCheck={false}
						/>
						<FormHelperText>
							{tt(
								"tsin.tokenHelp",
								"T.Sin's own API is plain HTTP with the token in the address, so anyone watching the network between this server and the provider can read it. The panel never shows the token again.",
							)}
						</FormHelperText>
					</FormControl>

					<FormControl>
						<FormLabel>{tt("tsin.dc", "Datacenter")}</FormLabel>
						<HStack spacing={2}>
							<Select
								value={dc}
								onChange={(event) => setDc(event.target.value)}
							>
								{(view?.datacenters ?? ["dc1"]).map((item) => (
									<option key={item} value={item}>
										{item}
									</option>
								))}
							</Select>
							<Button
								size="sm"
								variant="outline"
								flexShrink={0}
								isLoading={ranking}
								onClick={findBestDatacenter}
							>
								{tt("tsin.findBest", "Find the best")}
							</Button>
						</HStack>
						{ranked && (
							<Stack spacing={1} mt={2}>
								{ranked.map((item) => (
									<HStack key={item.dc} justify="space-between" fontSize="sm">
										<Text
											dir="ltr"
											fontWeight={item.dc === dc ? "bold" : "normal"}
										>
											{item.dc}
										</Text>
										{item.ok ? (
											<Badge colorScheme="green">
												{item.latency_ms} ms ·{" "}
												{tt("tsin.countriesCount", "{{count}} countries", {
													count: item.countries ?? 0,
												})}
											</Badge>
										) : (
											<Badge
												colorScheme={item.problem === "in_use" ? "blue" : "red"}
												title={item.error}
											>
												{item.problem
													? explainTSinProblem(item.problem, tt, nodeTunnel)
															.where
													: tt("tsin.dcUnavailable", "not available")}
											</Badge>
										)}
									</HStack>
								))}
							</Stack>
						)}
						{dcChangedOnNode && (
							<Alert status="warning" borderRadius="6px" fontSize="sm" mt={2}>
								<AlertIcon />
								<AlertDescription>
									{tt(
										"tsin.dcChangeOnNode",
										"The node's tunnel is registered on {{current}}. After applying {{next}}, run the new node script on that node right away, or every country stops working until you do.",
										{ current: view?.dc ?? "", next: dc },
									)}
								</AlertDescription>
							</Alert>
						)}
						<FormHelperText>
							{tt(
								"tsin.dcHelp",
								"Find the best asks every datacenter at once and picks the fastest one that accepts the token. On this panel's own machine a watch also checks the tunnel every minute and repairs it, moving to another datacenter only if it serves the same countries on the same ports.",
							)}
						</FormHelperText>
					</FormControl>

					{view?.account_error &&
						(() => {
							const code =
								view.account_error_code ||
								classifyTSinError(view.account_error) ||
								"unknown";
							// "In use" is the expected answer to the panel only while the
							// node does not report; in a node's report it is the node's own
							// registration being refused.
							const expected = nodeTunnel && !view.node_report;
							const explained = explainTSinProblem(code, tt, expected);
							return (
								<Alert
									status={expected && code === "in_use" ? "info" : "warning"}
									borderRadius="6px"
									fontSize="sm"
									alignItems="flex-start"
								>
									<AlertIcon />
									<Box>
										<Text fontWeight="semibold">{explained.title}</Text>
										<Text>{explained.hint}</Text>
										<Text
											fontSize="xs"
											opacity={0.7}
											mt={1}
											dir="ltr"
											fontFamily="mono"
										>
											{view.account_error}
										</Text>
									</Box>
								</Alert>
							);
						})()}

					{nodeTunnel && view?.node_report && (
						<Box borderWidth="1px" borderRadius="6px" p={3} fontSize="sm">
							<HStack justify="space-between" mb={2} flexWrap="wrap">
								<Text fontWeight="semibold">
									{tt("tsin.nodeReport.title", "What the node reports")}
								</Text>
								<HStack spacing={2}>
									<Badge
										colorScheme={
											!view.node_report.fresh
												? "orange"
												: view.node_report.tunnel_up
													? "green"
													: "red"
										}
									>
										{!view.node_report.fresh
											? tt("tsin.card.nodeSilent", "Node not reporting")
											: view.node_report.tunnel_up
												? tt("tsin.tunnelUp", "Tunnel up")
												: tt("tsin.card.tunnelDown", "Tunnel down")}
									</Badge>
									<Text fontSize="xs" opacity={0.7}>
										{tt("tsin.nodeReport.age", "{{count}}s ago", {
											count: view.node_report.age_seconds,
										})}
									</Text>
								</HStack>
							</HStack>
							{view.node_report.ports_total ? (
								<Text mb={1}>
									{tt(
										"tsin.nodeReport.ports",
										"{{working}} of {{total}} countries answer",
										{
											working: view.node_report.ports_working ?? 0,
											total: view.node_report.ports_total,
										},
									)}
									{(view.node_report.ports_failing ?? []).length > 0 && (
										<Text as="span" color="orange.400">
											{" — "}
											{(view.node_report.ports_failing ?? []).join(", ")}
										</Text>
									)}
								</Text>
							) : null}
							{(view.node_report.log ?? []).length > 0 && (
								<Box
									fontFamily="mono"
									fontSize="xs"
									opacity={0.8}
									dir="ltr"
									whiteSpace="pre-wrap"
									maxH="140px"
									overflowY="auto"
									mt={1}
								>
									{(view.node_report.log ?? []).join("\n")}
								</Box>
							)}
						</Box>
					)}

					{account && (
						<Box borderWidth="1px" borderRadius="6px" p={3}>
							<HStack justify="space-between" mb={2}>
								<Text fontWeight="semibold" dir="ltr">
									{account.client_name}
								</Text>
								<Badge colorScheme={account.active ? "green" : "red"}>
									{account.active
										? tt("tsin.active", "Active")
										: tt("tsin.inactive", "Inactive")}
								</Badge>
							</HStack>
							<Progress
								value={usedPercent}
								size="sm"
								borderRadius="4px"
								colorScheme={usedPercent > 90 ? "red" : "primary"}
							/>
							<SimpleGrid columns={2} spacing={2} mt={2} fontSize="sm">
								<Text dir="ltr">
									{formatGB(account.data_used_gb)} /{" "}
									{formatGB(account.data_limit_gb)}
								</Text>
								<Text>{expiryText(account.expiry_date, tt)}</Text>
							</SimpleGrid>
							<Text fontSize="xs" opacity={0.7} mt={1} dir="ltr">
								{account.expiry_date}
							</Text>
						</Box>
					)}

					{view?.applied && view.target === "master" && (
						<HStack fontSize="sm">
							<Badge colorScheme={view.tunnel.up ? "green" : "orange"}>
								{view.tunnel.up
									? tt("tsin.tunnelUp", "Tunnel up")
									: tt("tsin.tunnelDown", "Tunnel not connected")}
							</Badge>
							{view.last_detail && (
								<Text opacity={0.75}>{view.last_detail}</Text>
							)}
						</HStack>
					)}

					<Box>
						<Text fontWeight="semibold" mb={2}>
							{tt("tsin.countries", "Countries")} ({selected.length})
						</Text>
						<SimpleGrid columns={{ base: 1, sm: 2 }} spacing={1}>
							{names.map((name) => (
								<Checkbox
									key={name}
									isChecked={selected.includes(name)}
									onChange={() => toggle(name)}
								>
									{name}
								</Checkbox>
							))}
						</SimpleGrid>
						{!account && (
							<Text fontSize="xs" opacity={0.75} mt={2}>
								{tt(
									"tsin.countriesHint",
									"Enter the token and press Apply; the account's full country list appears afterwards.",
								)}
							</Text>
						)}
					</Box>

					{transport === "path" && selected.length > 0 && (
						<Box borderWidth="1px" borderRadius="6px" p={3}>
							<Text fontWeight="semibold" mb={1}>
								{tt("tsin.direct.title", "Direct, without Cloudflare ⚡")} (
								{direct.filter((name) => selected.includes(name)).length})
							</Text>
							<Text fontSize="sm" mb={2} opacity={0.85}>
								{tt(
									"tsin.direct.help",
									"These countries also get a second config, marked ⚡, that goes straight to the server's own IP over REALITY. If Cloudflare or the domain is blocked or slow (for example on Irancell), users switch to it. Pick a few main countries so the subscription stays short. Press Apply to save.",
								)}
							</Text>
							<SimpleGrid columns={{ base: 1, sm: 2 }} spacing={1}>
								{selected.map((name) => (
									<Checkbox
										key={name}
										isChecked={direct.includes(name)}
										onChange={() =>
											setDirect((current) =>
												current.includes(name)
													? current.filter((item) => item !== name)
													: [...current, name],
											)
										}
									>
										{name} ⚡
									</Checkbox>
								))}
							</SimpleGrid>
							<FormControl mt={3}>
								<FormLabel fontSize="sm" mb={1}>
									{tt("tsin.direct.address", "Separate IP for the ⚡ configs")}
								</FormLabel>
								<Input
									value={directAddress}
									onChange={(event) => setDirectAddress(event.target.value)}
									placeholder="203.0.113.7"
									dir="ltr"
									autoComplete="off"
									spellCheck={false}
								/>
								<FormHelperText fontSize="xs">
									{tt(
										"tsin.direct.addressHelp",
										"Recommended: buy an extra IP for this server from your provider and enter it here, so the server's main IP never appears in users' configs. If it gets filtered, buy another, enter it and press Apply; the Cloudflare configs keep working meanwhile. Empty uses the server's own IP. The ⚡ configs take the ports HTTPS sites use (443, 8443, 2053, …) when the node reports them free.",
									)}
								</FormHelperText>
							</FormControl>
						</Box>
					)}

					{view?.applied && (
						<Box
							borderWidth="1px"
							borderRadius="6px"
							p={3}
							borderColor={view.emergency?.enabled ? "red.400" : undefined}
						>
							<HStack justify="space-between" mb={1} spacing={3}>
								<Text fontWeight="semibold">
									{tt(
										"tsin.emergency.title",
										"🆘 Emergency tunnel (when T.Sin is down)",
									)}
								</Text>
								<HStack spacing={2} flexShrink={0}>
									<Text
										fontSize="sm"
										fontWeight="semibold"
										color={view.emergency?.enabled ? "red.400" : undefined}
									>
										{view.emergency?.enabled
											? tt("tsin.emergency.on", "On")
											: tt("tsin.emergency.off", "Off")}
									</Text>
									<Switch
										colorScheme="red"
										isChecked={Boolean(view.emergency?.enabled)}
										isDisabled={emBusy || !emAddress.trim()}
										onChange={(event) =>
											void saveEmergency(event.target.checked)
										}
									/>
								</HStack>
							</HStack>
							<Text fontSize="sm" mb={3} opacity={0.85}>
								{tt(
									"tsin.emergency.help",
									"For the hours T.Sin itself is down. Turned on, every user of the countries gets two more configs, “🆘 Emergency tunnel” (VMess and Shadowsocks), that reach this server through your Iranian tunnel and go out from here directly, without T.Sin: users show this server's country instead of their chosen one, but stay connected. Turn it off when T.Sin is back. Users update their subscription to get or drop the configs.",
								)}
							</Text>
							{view.emergency?.enabled && (
								<Alert status="error" borderRadius="6px" fontSize="sm" mb={3}>
									<AlertIcon />
									<AlertDescription>
										{tt(
											"tsin.emergency.onWarning",
											"The emergency tunnel is on. Its users go out from this server, not T.Sin. Turn it off once T.Sin works again.",
										)}
									</AlertDescription>
								</Alert>
							)}
							<SimpleGrid columns={{ base: 1, sm: 3 }} spacing={2}>
								<FormControl>
									<FormLabel fontSize="sm" mb={1}>
										{tt("tsin.emergency.address", "Iranian tunnel address")}
									</FormLabel>
									<Input
										value={emAddress}
										onChange={(event) => setEmAddress(event.target.value)}
										placeholder="185.x.x.x"
										dir="ltr"
										autoComplete="off"
										spellCheck={false}
									/>
								</FormControl>
								<FormControl>
									<FormLabel fontSize="sm" mb={1}>
										{tt("tsin.emergency.vmessPort", "Tunnel port for VMess")}
									</FormLabel>
									<Input
										value={emVMessPort}
										onChange={(event) => setEmVMessPort(event.target.value)}
										placeholder={tt(
											"tsin.emergency.samePort",
											"same as server",
										)}
										dir="ltr"
										inputMode="numeric"
									/>
								</FormControl>
								<FormControl>
									<FormLabel fontSize="sm" mb={1}>
										{tt("tsin.emergency.ssPort", "Tunnel port for Shadowsocks")}
									</FormLabel>
									<Input
										value={emSSPort}
										onChange={(event) => setEmSSPort(event.target.value)}
										placeholder={tt(
											"tsin.emergency.samePort",
											"same as server",
										)}
										dir="ltr"
										inputMode="numeric"
									/>
								</FormControl>
							</SimpleGrid>
							{view.emergency?.node_ports?.vmess ? (
								<Box mt={3} fontSize="sm">
									<Text mb={1}>
										{tt(
											"tsin.emergency.forward",
											"Set your Iranian tunnel to forward to these (TCP; for Shadowsocks also UDP):",
										)}
									</Text>
									<Text dir="ltr" fontFamily="mono" fontSize="xs">
										VMess → {view.emergency.node_address}:
										{view.emergency.node_ports.vmess}
										<br />
										Shadowsocks → {view.emergency.node_address}:
										{view.emergency.node_ports.shadowsocks}
									</Text>
								</Box>
							) : (
								<Text mt={3} fontSize="xs" opacity={0.75}>
									{tt(
										"tsin.emergency.firstSave",
										"Press Save once: the panel makes the two configs on this server and shows the ports your Iranian tunnel must forward to. Making them makes the server's users reconnect for a second, once.",
									)}
								</Text>
							)}
							<Button
								size="sm"
								mt={3}
								variant="outline"
								isLoading={emBusy}
								isDisabled={!emAddress.trim()}
								onClick={() => void saveEmergency()}
							>
								{tt("tsin.emergency.save", "Save")}
							</Button>
						</Box>
					)}

					{target !== "master" && view && (
						<Box borderWidth="1px" borderRadius="6px" p={3}>
							<Text fontWeight="semibold" mb={1}>
								{tt("tsin.scriptTitle", "Run this on the node")}
							</Text>
							<Text fontSize="sm" mb={2}>
								{tt(
									"tsin.scriptHelp",
									"After pressing Apply, run this script on the node's server. It asks for the token itself, so the token is never shown here. It brings up the tunnel, keeps it up, and reports its state to this panel every minute.",
								)}
							</Text>
							<Textarea
								value={nodeScript}
								isReadOnly
								rows={8}
								fontFamily="mono"
								fontSize="xs"
								dir="ltr"
							/>
							<Button size="sm" mt={2} onClick={script.onCopy}>
								{script.hasCopied
									? tt("tsin.copied", "Copied")
									: tt("tsin.copy", "Copy script")}
							</Button>
						</Box>
					)}

					{result && (
						<Alert
							status={result.warning ? "warning" : "success"}
							borderRadius="6px"
							fontSize="sm"
						>
							<AlertIcon />
							<AlertDescription>
								{tt(
									"tsin.assignHint",
									"Give users the service “{{name}}” to hand them these configs.",
									{
										name: result.service_name,
									},
								)}
							</AlertDescription>
						</Alert>
					)}
				</Stack>
			</AppDialog>
			<ConfirmDialog
				isOpen={confirmRemove}
				onClose={() => setConfirmRemove(false)}
				onConfirm={remove}
				title={tt("tsin.remove", "Remove T.Sin")}
				description={tt(
					"tsin.removeConfirm",
					"This deletes the country configs, their routing and the tunnel, and forgets the token.",
				)}
			/>
		</>
	);
};
