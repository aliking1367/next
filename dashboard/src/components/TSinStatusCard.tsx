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

// TSinStatusCard is a glance at the T.Sin account behind the country
// configs: traffic, time left, whether it is active, and the datacenter the
// tunnel uses. Clicking it opens the full Countries dialog.
export const TSinStatusCard: FC<{ onOpen: () => void }> = ({ onOpen }) => {
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
			refetchInterval: 120_000,
			refetchOnWindowFocus: false,
			retry: false,
		},
	);

	if (isLoading) {
		return (
			<Skeleton w="full" maxW={{ xl: "560px" }} h="78px" borderRadius="xl" />
		);
	}
	// Panels that never set T.Sin up see nothing here.
	if (!data || !data.token_set || !data.applied) return null;

	const account = data.account;
	const onNode = data.target !== "master";
	const nodeName = onNode
		? (data.targets?.find((item) => item.id === data.target)?.name ??
			data.target)
		: "";
	const dc = data.registered_dc && !onNode ? data.registered_dc : data.dc;

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

	// The status pill: the account first, then the tunnel this panel can see.
	let state: { label: string; color: string; pulse: boolean };
	if (!account) {
		state = {
			label: tt("tsin.card.unreachable", "No answer"),
			color: "red.400",
			pulse: false,
		};
	} else if (!account.active || (days !== null && days < 0)) {
		state = {
			label: tt("tsin.inactive", "Inactive"),
			color: "red.400",
			pulse: false,
		};
	} else if (!onNode && !data.tunnel.up) {
		state = {
			label: tt("tsin.tunnelDown", "Tunnel not connected"),
			color: "orange.400",
			pulse: false,
		};
	} else {
		state = {
			label: tt("tsin.card.connected", "Connected"),
			color: "green.400",
			pulse: true,
		};
	}

	const countries = data.countries?.length ?? 0;
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
			maxW={{ xl: "600px" }}
			borderWidth="1px"
			borderColor="panel.border"
			borderRadius="xl"
			bg="panel.elevated"
			px={4}
			py={3}
			cursor="pointer"
			transition="border-color 0.2s ease, transform 0.2s ease"
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
					>
						<Dot color={state.color} pulse={state.pulse} />
						<Text fontSize="xs" fontWeight="semibold" color={state.color}>
							{state.label}
						</Text>
					</HStack>
				</HStack>
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

			{account ? (
				<>
					<HStack justify="space-between" fontSize="xs" mb={1}>
						<Text color="panel.textMuted">
							{tt("tsin.card.traffic", "Traffic")}
						</Text>
						<Text dir="ltr" fontWeight="semibold">
							{gb(used)} / {limit > 0 ? `${gb(limit)} GB` : "∞"}
							<Text as="span" color="panel.textMuted" fontWeight="normal">
								{"  "}({percent.toFixed(0)}%)
							</Text>
						</Text>
					</HStack>
					<Progress
						value={percent}
						size="xs"
						borderRadius="full"
						colorScheme={barScheme}
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
							value={data.latency_ms ? `${dc} · ${data.latency_ms} ms` : dc}
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
									? tt("tsin.card.onNode", "On node")
									: tt("tsin.card.countries", "Countries")
							}
							value={onNode ? `${nodeName} · ${countries}` : countries}
						/>
					</HStack>
				</>
			) : (
				<Text fontSize="xs" color="red.300" dir="ltr" noOfLines={2}>
					{data.account_error ||
						tt("tsin.card.noAccount", "T.Sin did not answer")}
				</Text>
			)}
		</Box>
	);
};
