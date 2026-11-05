import {
	Box,
	HStack,
	Progress,
	Skeleton,
	Text,
	Tooltip,
	useColorModeValue,
} from "@chakra-ui/react";
import {
	ArrowPathIcon,
	CalendarDaysIcon,
	GlobeAltIcon,
	LinkIcon,
	ServerStackIcon,
	SignalIcon,
} from "@heroicons/react/24/outline";
import type { FC, MouseEvent, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { useQuery } from "react-query";
import { fetch } from "service/http";
import { classifyTSinError, explainTSinProblem } from "./tsinProblems";

type TSinNodeReport = {
	age_seconds: number;
	fresh: boolean;
	dc: string;
	tunnel_up: boolean;
	handshake_age: number;
	failures: number;
	log?: string[] | null;
	ports_total?: number;
	ports_working?: number;
	ports_failing?: string[] | null;
};

type TSinStatusView = {
	dc: string;
	registered_dc?: string;
	target: string;
	targets?: { id: string; name: string }[];
	token_set: boolean;
	applied: boolean;
	countries: string[] | null;
	latency_ms?: number;
	tunnel: { up: boolean; handshake?: string };
	account?: {
		client_name: string;
		data_used_gb: number;
		data_limit_gb: number;
		active_connections: number;
		expiry_date: string;
		active: boolean;
		proxies: unknown[] | null;
	};
	account_error?: string;
	account_error_code?: string;
	account_source?: "node";
	node_report?: TSinNodeReport;
};

export const tsinStatusQueryKey = "tsin-status";

// daysLeft reads T.Sin's expiry as the dialog does; null when there is none.
const daysLeft = (value: string) => {
	if (!value) return null;
	const when = new Date(value.replace(" ", "T"));
	if (Number.isNaN(when.getTime())) return null;
	return Math.ceil((when.getTime() - Date.now()) / 86_400_000);
};

const Dot: FC<{ color: string; pulse?: boolean }> = ({ color, pulse }) => (
	<Box position="relative" w="8px" h="8px" flexShrink={0}>
		{pulse && (
			<Box
				position="absolute"
				inset={0}
				borderRadius="full"
				bg={color}
				opacity={0.5}
				sx={{
					animation: "tsinPulse 2s ease-out infinite",
					"@keyframes tsinPulse": {
						"0%": { transform: "scale(1)", opacity: 0.5 },
						"100%": { transform: "scale(2.6)", opacity: 0 },
					},
				}}
			/>
		)}
		<Box position="absolute" inset={0} borderRadius="full" bg={color} />
	</Box>
);

const Fact: FC<{
	icon: typeof SignalIcon;
	label: string;
	value: ReactNode;
	color?: string;
}> = ({ icon, label, value, color }) => (
	<HStack spacing={2} minW={0}>
		<Box as={icon} w="16px" h="16px" color="panel.textMuted" flexShrink={0} />
		<Box minW={0}>
			<Text fontSize="10px" color="panel.textMuted" lineHeight="1.2">
				{label}
			</Text>
			<Text
				fontSize="sm"
				fontWeight="semibold"
				color={color}
				lineHeight="1.3"
				noOfLines={1}
				dir="ltr"
				textAlign="start"
			>
				{value}
			</Text>
		</Box>
	</HStack>
);

type CardState = {
	label: string;
	color: string;
	pulse: boolean;
	// problem explains a state that needs attention: where, and what to do.
	problem?: { title: string; hint: string; raw?: string };
};

// TSinStatusCard is a glance at the T.Sin account behind the country
// configs: traffic, time left, whether it works, the datacenter, and, when a
// country or the tunnel fails, where the problem is. Clicking it opens the
// full Countries dialog.
export const TSinStatusCard: FC<{
	onOpen: () => void;
	// wide fills its row instead of sitting in a header beside other things.
	wide?: boolean;
}> = ({ onOpen, wide }) => {
	const { t } = useTranslation();
	const tt = (
		key: string,
		fallback: string,
		options?: Record<string, unknown>,
	) => t(key, { defaultValue: fallback, ...options }) as string;
	const trackBg = useColorModeValue("blackAlpha.100", "whiteAlpha.100");

	const { data, isLoading, isFetching, refetch } = useQuery(
		[tsinStatusQueryKey],
		() => fetch<TSinStatusView>("/core/tsin"),
		{
			refetchInterval: 60_000,
			refetchOnWindowFocus: false,
			retry: false,
		},
	);

	if (isLoading) {
		// On a full-width row a placeholder would flash on panels without T.Sin.
		if (wide) return null;
		return (
			<Skeleton w="full" maxW={{ xl: "560px" }} h="78px" borderRadius="xl" />
		);
	}
	// Panels that never set T.Sin up see nothing here.
	if (!data || !data.token_set || !data.applied) return null;

	const account = data.account;
	const report = data.node_report;
	const onNode = data.target !== "master";
	const nodeName = onNode
		? (data.targets?.find((item) => item.id === data.target)?.name ??
			data.target)
		: "";
	const dc = (onNode ? report?.dc : data.registered_dc) || data.dc || "dc1";

	const used = account?.data_used_gb ?? 0;
	const limit = account?.data_limit_gb ?? 0;
	const percent = limit > 0 ? Math.min(100, (used / limit) * 100) : 0;
	const barScheme = percent >= 90 ? "red" : percent >= 75 ? "orange" : "green";
	const days = account ? daysLeft(account.expiry_date) : null;
	const daysColor =
		days === null
			? undefined
			: days <= 3
				? "red.400"
				: days <= 7
					? "orange.400"
					: undefined;
	const failing = report?.fresh ? (report.ports_failing ?? []) : [];
	const minutes = (seconds: number) => Math.max(1, Math.round(seconds / 60));

	// The state, worst first: the node gone quiet, the tunnel down, T.Sin
	// refusing, the account switched off, a few countries failing.
	const errorCode =
		data.account_error_code || classifyTSinError(data.account_error);
	let state: CardState;
	if (onNode && report && !report.fresh) {
		state = {
			label: tt("tsin.card.nodeSilent", "Node not reporting"),
			color: "orange.400",
			pulse: false,
			problem: {
				title: tt(
					"tsin.card.nodeSilentTitle",
					"No report from {{node}} for {{count}} min",
					{ node: nodeName, count: minutes(report.age_seconds) },
				),
				hint: tt(
					"tsin.card.nodeSilentHint",
					"The node is off, its internet is down, or next-tsin-watch stopped on it. Check the node; the users of the countries may be cut too.",
				),
			},
		};
	} else if (onNode && report?.fresh && !report.tunnel_up) {
		state = {
			label: tt("tsin.card.tunnelDown", "Tunnel down"),
			color: "red.400",
			pulse: false,
			problem: {
				title: tt(
					"tsin.card.tunnelDownTitle",
					"The T.Sin tunnel on {{node}} is down; the countries do not work",
					{ node: nodeName },
				),
				hint: errorCode
					? explainTSinProblem(errorCode, tt).hint
					: tt(
							"tsin.card.tunnelDownHint",
							"The node's watch is repairing it. The last lines of its log are in the Countries dialog.",
						),
				raw: report.log?.[report.log.length - 1] ?? data.account_error,
			},
		};
	} else if (!onNode && !data.tunnel.up) {
		state = {
			label: tt("tsin.card.tunnelDown", "Tunnel down"),
			color: "red.400",
			pulse: false,
			problem: {
				title: tt(
					"tsin.card.tunnelDownPanelTitle",
					"The T.Sin tunnel on this server is down; the countries do not work",
				),
				hint: errorCode
					? explainTSinProblem(errorCode, tt).hint
					: tt(
							"tsin.card.tunnelDownPanelHint",
							"The panel's watch is repairing it and reports to Telegram.",
						),
				raw: data.account_error,
			},
		};
	} else if (!account) {
		const explained = explainTSinProblem(
			errorCode || "unknown",
			tt,
			onNode && !report,
		);
		const expected = onNode && errorCode === "in_use" && !report;
		state = {
			label: expected
				? tt("tsin.card.waitingNode", "Waiting for the node")
				: explained.where,
			color: expected ? "blue.300" : "red.400",
			pulse: false,
			problem: {
				title: explained.title,
				hint: explained.hint,
				raw: data.account_error,
			},
		};
	} else if (!account.active || (days !== null && days < 0)) {
		state = {
			label: tt("tsin.inactive", "Inactive"),
			color: "red.400",
			pulse: false,
			problem: {
				title: tt(
					"tsin.card.inactiveTitle",
					"The T.Sin account is inactive or expired",
				),
				hint: tt(
					"tsin.card.inactiveHint",
					"The countries do not work until the account is renewed at T.Sin.",
				),
			},
		};
	} else if (failing.length > 0) {
		state = {
			label: tt("tsin.card.someFailing", "{{count}} country failing", {
				count: failing.length,
			}),
			color: "orange.400",
			pulse: true,
			problem: {
				title: failing.join(", "),
				hint: tt(
					"tsin.card.someFailingHint",
					"The tunnel works but T.Sin's proxy for these countries does not answer. The problem is on T.Sin's side; the other countries work.",
				),
			},
		};
	} else {
		state = {
			label: tt("tsin.card.connected", "Connected"),
			color: "green.400",
			pulse: true,
		};
	}

	const countries = data.countries?.length ?? 0;
	const countriesValue =
		report?.fresh && report.ports_total
			? `${report.ports_working ?? 0}/${report.ports_total}`
			: String(countries);
	const gb = (value: number) =>
		value >= 100 ? value.toFixed(0) : value.toFixed(1);

	return (
		<Box
			role="button"
			tabIndex={0}
			onClick={onOpen}
			onKeyDown={(event) => {
				if (event.key === "Enter" || event.key === " ") {
					event.preventDefault();
					onOpen();
				}
			}}
			w="full"
			maxW={wide ? undefined : { xl: "600px" }}
			borderWidth="1px"
			borderColor="panel.border"
			borderRadius="xl"
			bg="panel.elevated"
			px={4}
			py={3}
			cursor="pointer"
			transition="border-color 0.2s ease"
			_hover={{ borderColor: "primary.400" }}
			_focusVisible={{ outline: "2px solid", outlineColor: "primary.400" }}
			position="relative"
			overflow="hidden"
		>
			<Box
				position="absolute"
				insetInlineStart={0}
				top={0}
				bottom={0}
				w="3px"
				bg={state.color}
			/>
			<HStack justify="space-between" spacing={3} mb={2}>
				<HStack spacing={2} minW={0}>
					<Box as={GlobeAltIcon} w="18px" h="18px" color="primary.400" />
					<Text fontWeight="bold" fontSize="sm" noOfLines={1}>
						{tt("tsin.card.title", "Countries (T.Sin)")}
					</Text>
					<HStack
						spacing={1.5}
						px={2}
						py={0.5}
						borderRadius="full"
						bg={trackBg}
						flexShrink={0}
					>
						<Dot color={state.color} pulse={state.pulse} />
						<Text fontSize="xs" fontWeight="semibold" color={state.color}>
							{state.label}
						</Text>
					</HStack>
				</HStack>
				<HStack spacing={2} flexShrink={0}>
					{report?.fresh && (
						<Text fontSize="10px" color="panel.textMuted" noOfLines={1}>
							{tt("tsin.card.reported", "{{node}} · {{count}}s ago", {
								node: nodeName,
								count: report.age_seconds,
							})}
						</Text>
					)}
					<Tooltip label={tt("tsin.card.refresh", "Refresh")} hasArrow>
						<Box
							as="button"
							type="button"
							aria-label={tt("tsin.card.refresh", "Refresh")}
							onClick={(event: MouseEvent) => {
								event.stopPropagation();
								void refetch();
							}}
							color="panel.textMuted"
							_hover={{ color: "primary.400" }}
							display="flex"
						>
							<Box
								as={ArrowPathIcon}
								w="16px"
								h="16px"
								sx={
									isFetching
										? {
												animation: "tsinSpin 1s linear infinite",
												"@keyframes tsinSpin": {
													to: { transform: "rotate(360deg)" },
												},
											}
										: undefined
								}
							/>
						</Box>
					</Tooltip>
				</HStack>
			</HStack>

			{state.problem && (
				<Box
					mb={account ? 2 : 0}
					px={2.5}
					py={2}
					borderRadius="md"
					bg={trackBg}
				>
					<Text fontSize="xs" fontWeight="semibold" color={state.color}>
						{state.problem.title}
					</Text>
					<Text fontSize="xs" color="panel.textMuted" mt={0.5}>
						{state.problem.hint}
					</Text>
					{state.problem.raw && (
						<Text
							fontSize="10px"
							color="panel.textMuted"
							opacity={0.75}
							mt={1}
							dir="ltr"
							textAlign="start"
							noOfLines={2}
							fontFamily="mono"
						>
							{state.problem.raw}
						</Text>
					)}
				</Box>
			)}

			{account && (
				<>
					<HStack justify="space-between" fontSize="xs" mb={1}>
						<Text color="panel.textMuted">
							{tt("tsin.card.traffic", "Traffic")}
						</Text>
						<Text dir="ltr" fontWeight="semibold">
							{gb(used)} / {limit > 0 ? `${gb(limit)} GB` : "∞"}
							{limit > 0 && (
								<Text as="span" color="panel.textMuted" fontWeight="normal">
									{"  "}({percent.toFixed(0)}%)
								</Text>
							)}
						</Text>
					</HStack>
					<Progress
						value={limit > 0 ? percent : 100}
						size="xs"
						borderRadius="full"
						colorScheme={limit > 0 ? barScheme : "green"}
						bg={trackBg}
						mb={3}
					/>
					<HStack
						spacing={{ base: 3, md: 5 }}
						flexWrap="wrap"
						rowGap={2}
						justify="space-between"
					>
						<Fact
							icon={CalendarDaysIcon}
							label={tt("tsin.card.timeLeft", "Time left")}
							color={daysColor}
							value={
								days === null
									? "—"
									: days < 0
										? tt("tsin.expired", "Expired")
										: tt("tsin.card.days", "{{count}} days", { count: days })
							}
						/>
						<Fact
							icon={ServerStackIcon}
							label={tt("tsin.card.datacenter", "Datacenter")}
							value={
								!onNode && data.latency_ms
									? `${dc} · ${data.latency_ms} ms`
									: dc
							}
						/>
						<Fact
							icon={LinkIcon}
							label={tt("tsin.card.connections", "Connections")}
							value={account.active_connections}
						/>
						<Fact
							icon={SignalIcon}
							label={
								onNode
									? tt("tsin.card.onNodeWorking", "Working on {{node}}", {
											node: nodeName,
										})
									: tt("tsin.card.countries", "Countries")
							}
							color={failing.length > 0 ? "orange.400" : undefined}
							value={countriesValue}
						/>
					</HStack>
				</>
			)}
		</Box>
	);
};
