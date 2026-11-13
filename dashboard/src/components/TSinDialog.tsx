import {
	Alert,
	AlertDescription,
	AlertIcon,
	Badge,
	Box,
	Button,
	Checkbox,
	Collapse,
	chakra,
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
import { ChevronDownIcon } from "@heroicons/react/24/outline";
import { PanelSelect as Select } from "components/common/PanelSelect";
import { AppDialog } from "components/dialogs/AppDialog";
import { ConfirmDialog } from "components/dialogs/ConfirmDialog";
import { Input } from "components/Input";
import {
	type FC,
	type ReactNode,
	useCallback,
	useEffect,
	useState,
} from "react";
import { useTranslation } from "react-i18next";
import { fetch } from "service/http";
import { TSinLabSection } from "./TSinLabSection";
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

type TSinFastlyBackend = {
	country: string;
	tag: string;
	port: number;
	path: string;
};

type TSinFastlyCheck = {
	country: string;
	port: number;
	state: string;
	reason?: string;
	status?: number;
	detail?: string;
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
	// fastly_host is the Fastly service's domain; fastly_countries also get a
	// config through Fastly, and fastly_backends are the backends to create.
	fastly_host?: string;
	fastly_countries?: string[] | null;
	fastly_backends?: TSinFastlyBackend[] | null;
	// tunnel_countries maps a country (lower case) to the Iranian tunnel port
	// its 🛡 config goes through; tunnel_host is the tunnel's address (the
	// emergency tunnel's) and tunnel_per_port how many countries a port takes.
	tunnel_countries?: Record<string, number> | null;
	tunnel_host?: string;
	tunnel_per_port?: number;
	// tunnel_ports says, per port on the server, where users reach it: the
	// address the tunnel seller gave and the port on the tunnel's side.
	tunnel_ports?: { node_port: number; address: string; port: number }[] | null;
	origin_ip?: string;
	emergency?: TSinEmergency;
	// smart_config is the smart config switch: 0 off, 1 in the JSON
	// subscriptions apps already get, 2 also sending V2Box the JSON one.
	smart_config?: number;
	// front_script turns a new server into a front for the countries' server.
	front_script?: string;
	// iran_reach is the latest check, from test servers inside Iran, of the
	// address the direct configs point at.
	iran_reach?: {
		address: string;
		port: number;
		open: number;
		total: number;
		checked_at: string;
		error?: string;
	} | null;
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
	protocols?: "both" | "vmess" | "shadowsocks";
	// audience is "all", or "mci": shown only to users who update their
	// subscription from Hamrah Aval. mci_users counts those of the last
	// week; operator_ranges is how many MCI and Iran ranges are known.
	audience?: "all" | "mci";
	mci_users?: number;
	operator_ranges?: {
		mci_ranges: number;
		iran_ranges: number;
		fetched_at?: string;
	};
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

const SectionChevron = chakra(ChevronDownIcon, {
	baseStyle: { w: 4, h: 4, transition: "transform 0.2s" },
});

// TSinSection is a bordered block whose body folds away, so the dialog shows
// the countries first and the extra tools only when opened.
const TSinSection: FC<{
	title: ReactNode;
	status?: ReactNode;
	borderColor?: string;
	defaultOpen?: boolean;
	children: ReactNode;
}> = ({ title, status, borderColor, defaultOpen = false, children }) => {
	const [open, setOpen] = useState(defaultOpen);
	return (
		<Box borderWidth="1px" borderRadius="6px" borderColor={borderColor}>
			<HStack
				as="button"
				type="button"
				w="full"
				justify="space-between"
				spacing={3}
				p={3}
				textAlign="start"
				aria-expanded={open}
				onClick={() => setOpen((current) => !current)}
			>
				<Text fontWeight="semibold">{title}</Text>
				<HStack spacing={2} flexShrink={0}>
					{status}
					<SectionChevron transform={open ? "rotate(180deg)" : undefined} />
				</HStack>
			</HStack>
			<Collapse in={open} animateOpacity>
				<Box px={3} pb={3}>
					{children}
				</Box>
			</Collapse>
		</Box>
	);
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
	const [fastlyHost, setFastlyHost] = useState("");
	const [fastly, setFastly] = useState<string[]>([]);
	const [tunnelCountries, setTunnelCountries] = useState<
		Record<string, number>
	>({});
	// One row per tunnel port: the port on the server, and the address and
	// port the seller gave for it (empty: the emergency tunnel's address and
	// the same number).
	const [tunnelRows, setTunnelRows] = useState<
		{ id: string; nodePort: string; address: string; port: string }[]
	>([]);
	const [fastlyBusy, setFastlyBusy] = useState(false);
	const [fastlyResults, setFastlyResults] = useState<TSinFastlyCheck[] | null>(
		null,
	);
	const [emAddress, setEmAddress] = useState("");
	const [emVMessPort, setEmVMessPort] = useState("");
	const [emSSPort, setEmSSPort] = useState("");
	const [emBusy, setEmBusy] = useState(false);
	const [smartBusy, setSmartBusy] = useState(false);
	const [emProtocols, setEmProtocols] = useState("both");
	const [emAudience, setEmAudience] = useState("all");
	const [result, setResult] = useState<TSinApplyResponse | null>(null);
	const [confirmRemove, setConfirmRemove] = useState(false);
	const [ranking, setRanking] = useState(false);
	const [ranked, setRanked] = useState<TSinDatacenterRank[] | null>(null);
	// The node reports to the panel at the address the admin reaches it at.
	const nodeScript = (view?.node_script ?? "")
		.split("@PANEL_URL@")
		.join(window.location.origin);
	const script = useClipboard(nodeScript);
	const frontScript = useClipboard(view?.front_script ?? "");

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
			setFastlyHost(data.fastly_host ?? "");
			setFastly(data.fastly_countries ?? ["Best ping"]);
			const savedTunnel = data.tunnel_countries ?? {};
			setTunnelCountries(savedTunnel);
			const savedRows = (data.tunnel_ports ?? []).map((row) => ({
				id: `row-${row.node_port}`,
				nodePort: String(row.node_port),
				address: row.address ?? "",
				port: row.port ? String(row.port) : "",
			}));
			for (const port of Object.values(savedTunnel)) {
				if (
					port > 0 &&
					!savedRows.some((row) => row.nodePort === String(port))
				) {
					savedRows.push({
						id: `row-${port}`,
						nodePort: String(port),
						address: "",
						port: "",
					});
				}
			}
			if (savedRows.length === 0) {
				savedRows.push({
					id: "row-20100",
					nodePort: "20100",
					address: "",
					port: "",
				});
			}
			setTunnelRows(
				savedRows.sort(
					(a, b) =>
						Number.parseInt(a.nodePort, 10) - Number.parseInt(b.nodePort, 10),
				),
			);
			setEmAddress(data.emergency?.address ?? "");
			setEmProtocols(data.emergency?.protocols ?? "both");
			setEmAudience(data.emergency?.audience ?? "all");
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
				fastly_host: fastlyHost.trim(),
				fastly_countries: fastly.filter((name) => selected.includes(name)),
				tunnel_countries: Object.fromEntries(
					selected
						.map((name) => [name, tunnelCountries[name.toLowerCase()] ?? 0])
						.filter(([, port]) => Number(port) > 0),
				),
				tunnel_ports: tunnelRows
					.map((row) => ({
						node_port: Number.parseInt(row.nodePort, 10) || 0,
						address: row.address.trim(),
						port: Number.parseInt(row.port, 10) || 0,
					}))
					.filter((row) => row.node_port > 0 || row.address || row.port > 0),
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
				protocols: emProtocols,
				audience: emAudience,
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

	// saveSmart flips the smart config. It only changes what subscriptions
	// render, so nothing restarts; users get it on their next update.
	const saveSmart = async (level: number) => {
		if (smartBusy) return;
		setSmartBusy(true);
		try {
			const result = await fetch<{ level: number }>("/core/tsin/smart", {
				method: "POST",
				body: { level },
			});
			setView((current) =>
				current ? { ...current, smart_config: result.level } : current,
			);
			toast({
				status: "success",
				isClosable: true,
				duration: 8_000,
				title:
					result.level > 0
						? tt(
								"tsin.smart.turnedOn",
								"Smart config is on. Users get it when they update their subscription.",
							)
						: tt(
								"tsin.smart.turnedOff",
								"Smart config is off. It disappears when users update their subscription.",
							),
			});
		} catch (error: any) {
			toast({
				status: "error",
				isClosable: true,
				title: error?.data?.detail ?? String(error),
			});
		} finally {
			setSmartBusy(false);
		}
	};

	// checkFastly sends, through Fastly, the first request an XHTTP client
	// opens for each country, and shows what came back.
	const checkFastly = async () => {
		if (fastlyBusy) return;
		setFastlyBusy(true);
		try {
			const result = await fetch<{ results: TSinFastlyCheck[] }>(
				"/core/tsin/fastly-check",
				{ method: "POST" },
			);
			setFastlyResults(result.results);
		} catch (error: any) {
			toast({
				status: "error",
				isClosable: true,
				title: error?.data?.detail ?? String(error),
			});
		} finally {
			setFastlyBusy(false);
		}
	};

	const fastlyReason = (item: TSinFastlyCheck): string => {
		switch (item.reason) {
			case "dns":
				return tt(
					"tsin.fastly.reason.dns",
					"The Fastly domain does not resolve: check its spelling.",
				);
			case "no_service":
				return tt(
					"tsin.fastly.reason.noService",
					"Fastly does not know this domain or has no backend for this path (code {{code}}): check the domain and the condition.",
					{ code: item.status },
				);
			case "wrong_backend":
				return tt(
					"tsin.fastly.reason.wrongBackend",
					"The request reached the server on another port: this country's condition points at the wrong backend.",
				);
			case "backend_down":
				return tt(
					"tsin.fastly.reason.backendDown",
					"Fastly cannot reach the server (code {{code}}): check the address, the port, TLS on and certificate check off.",
					{ code: item.status },
				);
			case "unreachable":
				return tt(
					"tsin.fastly.reason.unreachable",
					"No answer from Fastly: {{detail}}",
					{ detail: item.detail ?? "" },
				);
		}
		return tt(
			"tsin.fastly.reason.other",
			"Unexpected answer (code {{code}}).",
			{
				code: item.status,
			},
		);
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
						<TSinSection
							title={tt("tsin.direct.title", "Direct, without Cloudflare ⚡")}
							status={
								<Badge>
									{direct.filter((name) => selected.includes(name)).length}
								</Badge>
							}
						>
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
							{view?.iran_reach && !view.iran_reach.error && (
								<Text
									fontSize="xs"
									mt={2}
									color={
										view.iran_reach.open === 0
											? "red.400"
											: view.iran_reach.open * 2 >= view.iran_reach.total
												? "green.400"
												: "orange.400"
									}
								>
									{tt(
										"tsin.front.iranReach",
										"Port check from Iranian datacenters: {{open}} of {{total}} opened {{address}} port {{port}}. This only shows the port is open; a mobile operator can still block the connection itself, so test the ⚡ configs on a phone.",
										{
											open: view.iran_reach.open,
											total: view.iran_reach.total,
											address: view.iran_reach.address,
											port: view.iran_reach.port,
										},
									)}
								</Text>
							)}
							{view?.front_script && (
								<Box mt={3} borderWidth="1px" borderRadius="6px" p={3}>
									<Text fontWeight="semibold" fontSize="sm" mb={1}>
										{tt(
											"tsin.front.title",
											"Front server (hides this server's IP)",
										)}
									</Text>
									<Text fontSize="xs" mb={2} opacity={0.85}>
										{tt(
											"tsin.front.help",
											"1) Buy a small separate server in Europe with unlimited traffic. 2) Log in to it with SSH as root, paste this script and press Enter; it hands every port except SSH on to this server and installs nothing else. 3) Put the new server's IP in the box above and press Apply. 4) Users update their subscription. If the front's IP is ever filtered, give it a new IP or a new server, run the script there, enter the new IP and Apply; this server is never touched.",
										)}
									</Text>
									<Textarea
										value={view.front_script}
										isReadOnly
										rows={5}
										fontFamily="mono"
										fontSize="xs"
										dir="ltr"
									/>
									<Button size="sm" mt={2} onClick={frontScript.onCopy}>
										{frontScript.hasCopied
											? tt("tsin.copied", "Copied")
											: tt("tsin.front.copy", "Copy front script")}
									</Button>
								</Box>
							)}
							{view?.applied && <TSinLabSection />}
						</TSinSection>
					)}

					{transport === "path" && selected.length > 0 && (
						<TSinSection
							title={tt("tsin.fastly.title", "Fastly, a second CDN 🅵")}
							status={
								<Badge
									colorScheme={
										view?.fastly_host && (view.fastly_backends?.length ?? 0) > 0
											? "green"
											: "gray"
									}
								>
									{view?.fastly_host
										? (view.fastly_backends?.length ?? 0)
										: tt("tsin.smart.off", "Off")}
								</Badge>
							}
						>
							<Text fontSize="sm" mb={2} opacity={0.85}>
								{tt(
									"tsin.fastly.help",
									"The chosen countries also get a config marked 🅵 that goes through Fastly instead of Cloudflare, over XHTTP. If Cloudflare is banned or blocked, users switch to it. Enter your Fastly service's domain, choose a few countries and press Apply; then make one backend per country in Fastly as shown below.",
								)}
							</Text>
							<FormControl mb={2}>
								<FormLabel fontSize="sm" mb={1}>
									{tt("tsin.fastly.host", "Fastly domain")}
								</FormLabel>
								<Input
									value={fastlyHost}
									onChange={(event) => setFastlyHost(event.target.value)}
									placeholder="name.global.ssl.fastly.net"
									dir="ltr"
									autoComplete="off"
									spellCheck={false}
								/>
								<FormHelperText fontSize="xs">
									{tt(
										"tsin.fastly.hostHelp",
										"The domain of your Fastly service, for example the free name.global.ssl.fastly.net. Empty removes the Fastly configs on the next Apply.",
									)}
								</FormHelperText>
							</FormControl>
							<SimpleGrid columns={{ base: 1, sm: 2 }} spacing={1}>
								{selected.map((name) => (
									<Checkbox
										key={name}
										isChecked={fastly.includes(name)}
										onChange={() =>
											setFastly((current) =>
												current.includes(name)
													? current.filter((item) => item !== name)
													: [...current, name],
											)
										}
									>
										{name} 🅵
									</Checkbox>
								))}
							</SimpleGrid>
							<Text fontSize="xs" mt={1} opacity={0.75}>
								{tt(
									"tsin.fastly.fewHelp",
									"Each country is one backend to create in Fastly, so start with Best ping alone.",
								)}
							</Text>

							{(view?.fastly_backends?.length ?? 0) > 0 && (
								<Box mt={3} borderWidth="1px" borderRadius="6px" p={3}>
									<Text fontWeight="semibold" fontSize="sm" mb={1}>
										{tt("tsin.fastly.setupTitle", "Setting Fastly up")}
									</Text>
									<Stack spacing={1} fontSize="xs" mb={2}>
										<Text>
											{tt(
												"tsin.fastly.setup1",
												"1) In Fastly, open your CDN service and press Edit configuration → Clone version.",
											)}
										</Text>
										<Text>
											{tt(
												"tsin.fastly.setup2",
												"2) Domains: the domain above must be listed.",
											)}
										</Text>
										<Text>
											{tt(
												"tsin.fastly.setup3",
												"3) Origins → Hosts: one host per row below, with its address and port, TLS: Yes, Verify certificate: No.",
											)}
										</Text>
										<Text>
											{tt(
												"tsin.fastly.setup4",
												"4) With more than one row, give each host the request condition shown, so each path reaches its own port.",
											)}
										</Text>
										<Text>
											{tt(
												"tsin.fastly.setup5",
												"5) Activate the version, wait a minute and press Check below.",
											)}
										</Text>
									</Stack>
									<Stack spacing={2}>
										{view?.fastly_backends?.map((backend) => (
											<Box
												key={backend.tag}
												borderWidth="1px"
												borderRadius="6px"
												p={2}
												fontSize="xs"
											>
												<Text fontWeight="semibold" mb={1}>
													{backend.country}
												</Text>
												<Text dir="ltr" fontFamily="mono">
													{tt("tsin.fastly.address", "Address")}:{" "}
													{view?.origin_ip ||
														tt("cdn.thisServerIP", "this server's IP")}
												</Text>
												<Text dir="ltr" fontFamily="mono">
													{tt("tsin.fastly.port", "Port")}: {backend.port}
												</Text>
												<Text dir="ltr" fontFamily="mono" wordBreak="break-all">
													{tt("tsin.fastly.condition", "Condition")}:
													req.url.path ~ "^{backend.path}"
												</Text>
											</Box>
										))}
									</Stack>
									<Button
										size="sm"
										mt={3}
										variant="outline"
										isLoading={fastlyBusy}
										onClick={() => void checkFastly()}
									>
										{tt("tsin.fastly.check", "Check through Fastly")}
									</Button>
									{fastlyResults && (
										<Stack spacing={1} mt={2}>
											{fastlyResults.map((item) => (
												<Text
													key={item.country}
													fontSize="xs"
													color={item.state === "ok" ? "green.400" : "red.400"}
												>
													{item.country}:{" "}
													{item.state === "ok"
														? tt("tsin.fastly.ok", "works through Fastly")
														: fastlyReason(item)}
												</Text>
											))}
										</Stack>
									)}
								</Box>
							)}
						</TSinSection>
					)}

					{transport === "path" && selected.length > 0 && (
						<TSinSection
							title={tt(
								"tsin.tunnelCountries.title",
								"🛡 Countries through the Iranian tunnel",
							)}
							status={
								<Badge
									colorScheme={
										Object.keys(view?.tunnel_countries ?? {}).length > 0
											? "green"
											: "gray"
									}
								>
									{Object.keys(view?.tunnel_countries ?? {}).length ||
										tt("tsin.smart.off", "Off")}
								</Badge>
							}
						>
							<Text fontSize="sm" mb={2} opacity={0.85}>
								{tt(
									"tsin.tunnelCountries.help",
									"The chosen countries also get a config marked 🛡 that goes through an Iranian tunnel instead of Cloudflare: VMess over WebSocket, up to {{max}} countries on one tunnel port, told apart by path. Add a row for each tunnel port with the address and port the seller gave, choose a port for each country and press Apply. With the emergency tunnel shown to Hamrah Aval only, these configs are too.",
									{ max: view?.tunnel_per_port ?? 5 },
								)}
							</Text>
							{!view?.tunnel_host &&
								tunnelRows.every((row) => !row.address.trim()) && (
									<Text fontSize="sm" mb={2} color="orange.400">
										{tt(
											"tsin.tunnelCountries.noHost",
											"Enter the tunnel address in each row below, or the emergency tunnel's address, which rows without one use.",
										)}
									</Text>
								)}
							{view?.tunnel_host && (
								<Text fontSize="xs" mb={2} dir="ltr" opacity={0.8}>
									{view.tunnel_host}
								</Text>
							)}
							<Text fontSize="sm" fontWeight="semibold" mb={1}>
								{tt("tsin.tunnelCountries.ports", "Tunnel ports")}
							</Text>
							<Text fontSize="xs" mb={2} opacity={0.8}>
								{tt(
									"tsin.tunnelCountries.portsHelp",
									"One row per port bought on the tunnel. Port on the server: where the tunnel forwards to on the countries' server. Tunnel address and tunnel port: what the seller gave (empty address uses the emergency tunnel's address, empty port the same number).",
								)}
							</Text>
							<Stack spacing={2} mb={3}>
								{tunnelRows.map((row, index) => {
									const update = (patch: Partial<typeof row>) =>
										setTunnelRows((current) =>
											current.map((item, i) =>
												i === index ? { ...item, ...patch } : item,
											),
										);
									return (
										<SimpleGrid
											key={row.id}
											columns={{ base: 1, sm: 4 }}
											spacing={2}
											alignItems="end"
										>
											<FormControl>
												<FormLabel fontSize="xs" mb={0.5}>
													{tt(
														"tsin.tunnelCountries.nodePort",
														"Port on the server",
													)}
												</FormLabel>
												<Input
													size="sm"
													value={row.nodePort}
													onChange={(event) =>
														update({ nodePort: event.target.value })
													}
													placeholder="20100"
													dir="ltr"
													inputMode="numeric"
												/>
											</FormControl>
											<FormControl>
												<FormLabel fontSize="xs" mb={0.5}>
													{tt("tsin.tunnelCountries.address", "Tunnel address")}
												</FormLabel>
												<Input
													size="sm"
													value={row.address}
													onChange={(event) =>
														update({ address: event.target.value })
													}
													placeholder={view?.tunnel_host || "tun.example.ir"}
													dir="ltr"
													autoComplete="off"
													spellCheck={false}
												/>
											</FormControl>
											<FormControl>
												<FormLabel fontSize="xs" mb={0.5}>
													{tt("tsin.tunnelCountries.remotePort", "Tunnel port")}
												</FormLabel>
												<Input
													size="sm"
													value={row.port}
													onChange={(event) =>
														update({ port: event.target.value })
													}
													placeholder={row.nodePort || "20100"}
													dir="ltr"
													inputMode="numeric"
												/>
											</FormControl>
											<Button
												size="sm"
												variant="ghost"
												colorScheme="red"
												onClick={() => {
													const port = Number.parseInt(row.nodePort, 10);
													setTunnelRows((current) =>
														current.filter((_, i) => i !== index),
													);
													setTunnelCountries((current) =>
														Object.fromEntries(
															Object.entries(current).filter(
																([, value]) => value !== port,
															),
														),
													);
												}}
											>
												{tt("tsin.tunnelCountries.removeRow", "Remove")}
											</Button>
										</SimpleGrid>
									);
								})}
								<Button
									size="sm"
									variant="outline"
									alignSelf="start"
									onClick={() =>
										setTunnelRows((current) => {
											const highest = current.reduce(
												(max, row) =>
													Math.max(max, Number.parseInt(row.nodePort, 10) || 0),
												20099,
											);
											return [
												...current,
												{
													id: `row-${Date.now()}`,
													nodePort: String(highest + 1),
													address: "",
													port: "",
												},
											];
										})
									}
								>
									{tt("tsin.tunnelCountries.addRow", "+ Add a tunnel port")}
								</Button>
							</Stack>
							<Stack spacing={1}>
								{selected.map((name) => {
									const key = name.toLowerCase();
									const ports = tunnelRows
										.map((row) => Number.parseInt(row.nodePort, 10))
										.filter((port) => port > 0 && port <= 65535);
									const max = view?.tunnel_per_port ?? 5;
									const used = (port: number) =>
										selected.filter(
											(other) => tunnelCountries[other.toLowerCase()] === port,
										).length;
									return (
										<HStack key={name} justify="space-between" spacing={2}>
											<Text fontSize="sm">{name} 🛡</Text>
											<Box w="150px" flexShrink={0}>
												<Select
													size="sm"
													value={String(tunnelCountries[key] ?? 0)}
													onChange={(event) => {
														const port =
															Number.parseInt(event.target.value, 10) || 0;
														setTunnelCountries((current) => {
															const next = { ...current };
															if (port > 0) next[key] = port;
															else delete next[key];
															return next;
														});
													}}
												>
													<option value="0">—</option>
													{ports.map((port) => (
														<option
															key={port}
															value={String(port)}
															disabled={
																tunnelCountries[key] !== port &&
																used(port) >= max
															}
														>
															{port} ({used(port)}/{max})
														</option>
													))}
												</Select>
											</Box>
										</HStack>
									);
								})}
							</Stack>
						</TSinSection>
					)}

					{view?.applied &&
						view.transport === "path" &&
						(view.direct_countries?.length ?? 0) > 0 && (
							<TSinSection
								title={tt("tsin.smart.title", "⭐ Smart config")}
								status={
									<Badge colorScheme={view.smart_config ? "green" : "gray"}>
										{view.smart_config
											? tt("tsin.smart.on", "On")
											: tt("tsin.smart.off", "Off")}
									</Badge>
								}
							>
								<Text fontSize="sm" mb={2} opacity={0.85}>
									{tt(
										"tsin.smart.help",
										"Adds one config, «⭐ Smart (best location)», at the top of the subscription. It goes over ⚡ and, if ⚡ stops answering, moves to Cloudflare by itself within seconds, so users stay connected without changing config. Only apps that read JSON subscriptions show it; the other configs stay as they are. Users get it when they update their subscription; nothing restarts.",
									)}
								</Text>
								<FormControl>
									<Select
										size="sm"
										value={String(view.smart_config ?? 0)}
										isDisabled={smartBusy}
										onChange={(event) =>
											saveSmart(Number.parseInt(event.target.value, 10) || 0)
										}
									>
										<option value="0">{tt("tsin.smart.level0", "Off")}</option>
										<option value="1">
											{tt(
												"tsin.smart.level1",
												"On for Happ, Streisand and other JSON apps",
											)}
										</option>
										{view.smart_config === 2 && (
											<option value="2">
												{tt(
													"tsin.smart.level2",
													"On, and V2Box also gets the JSON subscription",
												)}
											</option>
										)}
									</Select>
									<FormHelperText fontSize="xs">
										{tt(
											"tsin.smart.testHelp",
											"Test first on your own phone: add ?smart=1 to the end of a subscription link (?smart=fail turns ⚡ off, ?smart=nocdn turns Cloudflare off, to see each way carry the traffic alone).",
										)}
									</FormHelperText>
								</FormControl>
								{view.smart_config === 2 && (
									<Text fontSize="xs" mt={2} color="orange.400">
										{tt(
											"tsin.smart.v2boxNote",
											"V2Box users now get their subscription as JSON. All configs stay, but after the update the app may ask them to pick a config again.",
										)}
									</Text>
								)}
								<Text fontSize="xs" mt={2} opacity={0.75}>
									{tt(
										"tsin.smart.v2boxUnsupported",
										"V2Box does not run the smart config's check, so it is not offered there. The smart config only helps where the ⚡ configs connect on users' networks.",
									)}
								</Text>
							</TSinSection>
						)}

					{view?.applied && (
						<TSinSection
							title={tt(
								"tsin.emergency.title",
								"🆘 Emergency tunnel (when T.Sin is down)",
							)}
							borderColor={view.emergency?.enabled ? "red.400" : undefined}
							defaultOpen={Boolean(view.emergency?.enabled)}
							status={
								<Badge colorScheme={view.emergency?.enabled ? "red" : "gray"}>
									{view.emergency?.enabled
										? tt("tsin.emergency.on", "On")
										: tt("tsin.emergency.off", "Off")}
								</Badge>
							}
						>
							<HStack justify="flex-end" mb={2} spacing={3}>
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
							<FormControl mb={2}>
								<FormLabel fontSize="sm" mb={1}>
									{tt("tsin.emergency.audience", "Show to")}
								</FormLabel>
								<Select
									size="sm"
									value={emAudience}
									onChange={(event) => setEmAudience(event.target.value)}
								>
									<option value="all">
										{tt("tsin.emergency.audienceAll", "Every user")}
									</option>
									<option value="mci">
										{tt(
											"tsin.emergency.audienceMci",
											"Only users on Hamrah Aval",
										)}
									</option>
								</Select>
								<FormHelperText fontSize="xs">
									{tt(
										"tsin.emergency.audienceHelp",
										"With Hamrah Aval only, the configs go into the subscription only when it is updated from Hamrah Aval's network; on Irancell, fixed lines or Wi-Fi they are left out, so the tunnel's paid traffic goes to the users who need it. An update made through a VPN keeps them for a week for someone last seen on Hamrah Aval. Users must update their subscription for a change to reach them; press Save.",
									)}
								</FormHelperText>
								{emAudience === "mci" && (
									<Text fontSize="xs" mt={1} opacity={0.85}>
										{view?.emergency?.operator_ranges?.mci_ranges
											? tt(
													"tsin.emergency.mciUsers",
													"{{count}} users updated their subscription from Hamrah Aval in the last week.",
													{ count: view.emergency.mci_users ?? 0 },
												)
											: tt(
													"tsin.emergency.rangesLoading",
													"Hamrah Aval's address list is still loading; until then the configs go to everyone.",
												)}
									</Text>
								)}
							</FormControl>
							<FormControl mb={2}>
								<FormLabel fontSize="sm" mb={1}>
									{tt("tsin.emergency.protocols", "Configs to show")}
								</FormLabel>
								<Select
									size="sm"
									value={emProtocols}
									onChange={(event) => setEmProtocols(event.target.value)}
								>
									<option value="both">
										{tt(
											"tsin.emergency.both",
											"VMess and Shadowsocks (two tunnel ports)",
										)}
									</option>
									<option value="vmess">
										{tt(
											"tsin.emergency.vmessOnly",
											"VMess only (one tunnel port)",
										)}
									</option>
									<option value="shadowsocks">
										{tt(
											"tsin.emergency.ssOnly",
											"Shadowsocks only (one tunnel port)",
										)}
									</option>
								</Select>
								<FormHelperText fontSize="xs">
									{tt(
										"tsin.emergency.protocolsHelp",
										"Each tunnel port you buy reaches one config. With one port, pick one: VMess works on every tunnel; Shadowsocks also carries calls if the tunnel forwards UDP.",
									)}
								</FormHelperText>
							</FormControl>
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
								<FormControl
									display={emProtocols === "shadowsocks" ? "none" : undefined}
								>
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
								<FormControl
									display={emProtocols === "vmess" ? "none" : undefined}
								>
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
									{emProtocols !== "shadowsocks" && (
										<Text dir="ltr" fontFamily="mono" fontSize="xs">
											VMess → {view.emergency.node_address}:
											{view.emergency.node_ports.vmess}
										</Text>
									)}
									{emProtocols !== "vmess" && (
										<Text dir="ltr" fontFamily="mono" fontSize="xs">
											Shadowsocks → {view.emergency.node_address}:
											{view.emergency.node_ports.shadowsocks}
										</Text>
									)}
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
						</TSinSection>
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
