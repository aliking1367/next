import {
	Badge,
	Box,
	Button,
	ButtonGroup,
	Flex,
	HStack,
	Select,
	SimpleGrid,
	Spinner,
	Table,
	Tbody,
	Td,
	Text,
	Th,
	Thead,
	Tr,
	useColorMode,
	VStack,
} from "@chakra-ui/react";
import type { ApexOptions } from "apexcharts";
import { type FC, useState } from "react";
import ReactApexChart from "react-apexcharts";
import { useTranslation } from "react-i18next";
import { useQuery } from "react-query";
import { fetch } from "service/http";
import { formatBytes } from "utils/formatByte";

type Granularity = "hour" | "day" | "week" | "month";

type Totals = { up: number; down: number };

type Summary = {
	today: Totals;
	last_24h: Totals;
	last_7d: Totals;
	last_30d: Totals;
	total: Totals;
	hourly_profile: number[];
	peak_hours: { hour: number; avg_bytes: number; avg_mbps: number }[];
	peak_avg_mbps: number;
	daily_avg_bytes: number;
	busiest?: { start: string; label: string; bytes: number; mbps: number };
	profile_days: number;
	series: { start: string; label: string; up: number; down: number }[];
};

type Tunnel = Summary & {
	tag: string;
	label: string;
	address: string;
	port: number;
	node_port: number;
	countries: string[];
	emergency: boolean;
};

type TunnelUsageResponse = {
	granularity: Granularity;
	tunnels: Tunnel[];
	all: Summary;
};

const sum = (value?: Totals) => (value ? value.up + value.down : 0);
const hourRange = (hour: number) =>
	`${String(hour).padStart(2, "0")}:00–${String((hour + 1) % 24).padStart(2, "0")}:00`;
// SummaryLine puts a label above its value, the value kept
// left-to-right so numbers and units stay in order in a right-to-left page.
const SummaryLine: FC<{ label: string; value: string }> = ({
	label,
	value,
}) => (
	<Box py={0.5}>
		<Text>{label}</Text>
		<Text fontWeight="bold" color="panel.text" dir="ltr" textAlign="end">
			{value}
		</Text>
	</Box>
);

const mbps = (value: number) =>
	`${value < 10 ? value.toFixed(1) : Math.round(value)} Mbps`;

// TunnelUsageCard shows how much traffic each Iranian tunnel carried, named
// by the address users reach it on: recent totals, a chart by hour, day,
// week or month, and the hours of the day it is busiest, in Iran's time.
// It shows nothing on a panel without tunnels.
export const TunnelUsageCard: FC = () => {
	const { t, i18n } = useTranslation();
	const tt = (
		key: string,
		fallback: string,
		options?: Record<string, unknown>,
	) => t(key, { defaultValue: fallback, ...options }) as string;
	const { colorMode } = useColorMode();
	const [granularity, setGranularity] = useState<Granularity>("hour");
	const [selected, setSelected] = useState("all");
	const calendar = i18n.language?.startsWith("fa") ? "jalali" : "gregorian";

	const { data, isLoading } = useQuery(
		["tunnel-usage", granularity, calendar],
		() =>
			fetch<TunnelUsageResponse>(
				`/core/tsin/tunnel-usage?granularity=${granularity}&calendar=${calendar}`,
			),
		{
			refetchInterval: 5 * 60_000,
			refetchOnWindowFocus: false,
			retry: false,
			keepPreviousData: true,
		},
	);

	const tunnel = data?.tunnels.find((item) => item.tag === selected);
	const summary: Summary | undefined = tunnel ?? data?.all;
	const axisColor = colorMode === "dark" ? "#A0AEC0" : "#718096";

	const seriesChart = (() => {
		const points = summary?.series ?? [];
		const categories = points.map((point) =>
			granularity === "hour" ? point.label.slice(-5) : point.label,
		);
		const options: ApexOptions = {
			chart: {
				type: "bar",
				stacked: true,
				toolbar: { show: false },
				background: "transparent",
				fontFamily: "inherit",
			},
			plotOptions: { bar: { borderRadius: 3, columnWidth: "65%" } },
			dataLabels: { enabled: false },
			grid: { borderColor: colorMode === "dark" ? "#2D3748" : "#E2E8F0" },
			xaxis: {
				categories,
				labels: {
					rotate: -45,
					hideOverlappingLabels: true,
					style: { colors: categories.map(() => axisColor), fontSize: "10px" },
				},
				axisBorder: { show: false },
				axisTicks: { show: false },
				tickAmount: Math.min(categories.length, 12),
			},
			yaxis: {
				labels: {
					formatter: (value) => formatBytes(Number(value) || 0, 1),
					style: { colors: [axisColor] },
				},
			},
			legend: { labels: { colors: axisColor } },
			tooltip: {
				theme: colorMode === "dark" ? "dark" : "light",
				shared: true,
				intersect: false,
				x: {
					formatter: (_value, opts) =>
						points[opts?.dataPointIndex ?? 0]?.label ?? "",
				},
				y: { formatter: (value) => formatBytes(Number(value) || 0, 2) },
			},
			colors: [
				colorMode === "dark" ? "#63B3ED" : "#3182CE",
				colorMode === "dark" ? "#F6AD55" : "#DD6B20",
			],
		};
		return {
			options,
			series: [
				{
					name: tt("tunnelUsage.download", "Download"),
					data: points.map((point) => point.down),
				},
				{
					name: tt("tunnelUsage.upload", "Upload"),
					data: points.map((point) => point.up),
				},
			],
		};
	})();

	const profileChart = (() => {
		const profile = summary?.hourly_profile ?? [];
		const peaks = new Set((summary?.peak_hours ?? []).map((peak) => peak.hour));
		const base = colorMode === "dark" ? "#4A5568" : "#CBD5E0";
		const hot = colorMode === "dark" ? "#FC8181" : "#E53E3E";
		const options: ApexOptions = {
			chart: {
				type: "bar",
				toolbar: { show: false },
				background: "transparent",
				fontFamily: "inherit",
			},
			plotOptions: {
				bar: { borderRadius: 2, columnWidth: "70%", distributed: true },
			},
			dataLabels: { enabled: false },
			legend: { show: false },
			grid: { borderColor: colorMode === "dark" ? "#2D3748" : "#E2E8F0" },
			xaxis: {
				categories: profile.map((_, hour) => String(hour)),
				labels: {
					style: { colors: profile.map(() => axisColor), fontSize: "10px" },
				},
				axisBorder: { show: false },
				axisTicks: { show: false },
			},
			yaxis: {
				labels: {
					formatter: (value) => formatBytes(Number(value) || 0, 0),
					style: { colors: [axisColor] },
				},
			},
			tooltip: {
				theme: colorMode === "dark" ? "dark" : "light",
				x: {
					formatter: (_value, opts) => hourRange(opts?.dataPointIndex ?? 0),
				},
				y: {
					formatter: (value) =>
						`${formatBytes(Number(value) || 0, 2)} · ${mbps(((Number(value) || 0) * 8) / 3600 / 1e6)}`,
				},
			},
			colors: profile.map((_, hour) => (peaks.has(hour) ? hot : base)),
		};
		return {
			options,
			series: [
				{
					name: tt("tunnelUsage.avgPerHour", "Average per hour"),
					data: profile,
				},
			],
		};
	})();

	if (!isLoading && !data?.tunnels.length) return null;

	const stats: { key: string; label: string; value?: Totals }[] = [
		{
			key: "today",
			label: tt("tunnelUsage.today", "Today"),
			value: summary?.today,
		},
		{
			key: "24h",
			label: tt("tunnelUsage.last24h", "Last 24 hours"),
			value: summary?.last_24h,
		},
		{
			key: "7d",
			label: tt("tunnelUsage.last7d", "Last 7 days"),
			value: summary?.last_7d,
		},
		{
			key: "30d",
			label: tt("tunnelUsage.last30d", "Last 30 days"),
			value: summary?.last_30d,
		},
		{
			key: "total",
			label: tt("tunnelUsage.total", "Since counters reset"),
			value: summary?.total,
		},
	];
	const granularities: { key: Granularity; label: string }[] = [
		{ key: "hour", label: tt("tunnelUsage.hourly", "Hourly") },
		{ key: "day", label: tt("tunnelUsage.daily", "Daily") },
		{ key: "week", label: tt("tunnelUsage.weekly", "Weekly") },
		{ key: "month", label: tt("tunnelUsage.monthly", "Monthly") },
	];

	return (
		<Box
			bg="panel.surface"
			borderWidth="1px"
			borderColor="panel.border"
			borderRadius="20px"
			p={{ base: 3, md: 5 }}
		>
			<Flex
				justify="space-between"
				align={{ base: "stretch", md: "center" }}
				gap={3}
				direction={{ base: "column", md: "row" }}
				mb={4}
			>
				<VStack align="start" spacing={0.5}>
					<Text fontWeight="semibold">
						{tt("tunnelUsage.title", "🛡 Tunnel usage")}
					</Text>
					<Text fontSize="xs" color="panel.textMuted">
						{tt(
							"tunnelUsage.subtitle",
							"Traffic through each Iranian tunnel, in Iran's time",
						)}
					</Text>
				</VStack>
				<HStack spacing={2} flexWrap="wrap">
					<Select
						size="sm"
						maxW={{ base: "full", md: "260px" }}
						value={selected}
						onChange={(event) => setSelected(event.target.value)}
						borderRadius="lg"
					>
						<option value="all">
							{tt("tunnelUsage.allTunnels", "All tunnels")}
						</option>
						{data?.tunnels.map((item) => (
							<option key={item.tag} value={item.tag}>
								{item.label}
							</option>
						))}
					</Select>
					<ButtonGroup size="sm" isAttached variant="outline">
						{granularities.map((item) => (
							<Button
								key={item.key}
								onClick={() => setGranularity(item.key)}
								isActive={granularity === item.key}
								fontSize="xs"
							>
								{item.label}
							</Button>
						))}
					</ButtonGroup>
				</HStack>
			</Flex>

			{isLoading || !summary ? (
				<Flex justify="center" py={10}>
					<Spinner />
				</Flex>
			) : (
				<VStack align="stretch" spacing={4}>
					{tunnel && tunnel.countries.length > 0 && (
						<HStack spacing={1} flexWrap="wrap">
							{tunnel.countries.map((country) => (
								<Badge
									key={country}
									textTransform="capitalize"
									variant="subtle"
									colorScheme="blue"
								>
									{country}
								</Badge>
							))}
						</HStack>
					)}

					<SimpleGrid columns={{ base: 2, md: 5 }} gap={3}>
						{stats.map((stat) => (
							<Box
								key={stat.key}
								borderWidth="1px"
								borderColor="panel.border"
								borderRadius="xl"
								p={3}
								bg="panel.elevated"
							>
								<Text fontSize="xs" color="panel.textMuted">
									{stat.label}
								</Text>
								<Text
									fontSize="lg"
									fontWeight="bold"
									dir="ltr"
									textAlign="start"
								>
									{formatBytes(sum(stat.value), 2)}
								</Text>
								<Text
									fontSize="10px"
									color="panel.textMuted"
									dir="ltr"
									textAlign="start"
								>
									↓ {formatBytes(stat.value?.down ?? 0, 1)} · ↑{" "}
									{formatBytes(stat.value?.up ?? 0, 1)}
								</Text>
							</Box>
						))}
					</SimpleGrid>

					<Box>
						<Text fontSize="sm" fontWeight="semibold" mb={1}>
							{granularities.find((item) => item.key === granularity)?.label}
						</Text>
						<ReactApexChart
							options={seriesChart.options}
							series={seriesChart.series}
							type="bar"
							height={260}
						/>
					</Box>

					<SimpleGrid columns={{ base: 1, lg: 3 }} gap={4}>
						<Box gridColumn={{ lg: "span 2" }}>
							<Text fontSize="sm" fontWeight="semibold">
								{tt("tunnelUsage.profile", "Average by hour of day")}
							</Text>
							<Text fontSize="xs" color="panel.textMuted">
								{tt(
									"tunnelUsage.profileHint",
									"Over the last {{count}} day(s); the busiest hours are in red",
									{
										count: summary.profile_days || 0,
									},
								)}
							</Text>
							<ReactApexChart
								options={profileChart.options}
								series={profileChart.series}
								type="bar"
								height={200}
							/>
						</Box>
						<VStack
							align="stretch"
							spacing={2}
							borderWidth="1px"
							borderColor="panel.border"
							borderRadius="xl"
							p={3}
						>
							<Text fontSize="sm" fontWeight="semibold">
								{tt("tunnelUsage.peakHours", "Peak hours")}
							</Text>
							{summary.peak_hours.length === 0 ? (
								<Text fontSize="xs" color="panel.textMuted">
									{tt(
										"tunnelUsage.noHistory",
										"No history yet; it fills in as users connect.",
									)}
								</Text>
							) : (
								<>
									{summary.peak_hours.map((peak) => (
										<Flex
											key={peak.hour}
											justify="space-between"
											fontSize="sm"
											gap={2}
										>
											<Text dir="ltr">{hourRange(peak.hour)}</Text>
											<Text dir="ltr" color="panel.textMuted">
												{formatBytes(peak.avg_bytes, 2)} · {mbps(peak.avg_mbps)}
											</Text>
										</Flex>
									))}
									<Box
										borderTopWidth="1px"
										borderColor="panel.border"
										pt={2}
										fontSize="xs"
										color="panel.textMuted"
									>
										<SummaryLine
											label={tt(
												"tunnelUsage.peakAvg",
												"Average speed in peak hours",
											)}
											value={mbps(summary.peak_avg_mbps)}
										/>
										<SummaryLine
											label={tt("tunnelUsage.dailyAvg", "Average per day")}
											value={formatBytes(summary.daily_avg_bytes, 2)}
										/>
										{summary.busiest && (
											<SummaryLine
												label={tt("tunnelUsage.busiest", "Busiest hour")}
												value={`${summary.busiest.label} · ${formatBytes(summary.busiest.bytes, 2)} · ${mbps(summary.busiest.mbps)}`}
											/>
										)}
									</Box>
								</>
							)}
						</VStack>
					</SimpleGrid>

					{selected === "all" && (data?.tunnels.length ?? 0) > 1 && (
						<Box overflowX="auto">
							<Table size="sm">
								<Thead>
									<Tr>
										<Th>{tt("tunnelUsage.tunnel", "Tunnel")}</Th>
										<Th isNumeric>{tt("tunnelUsage.today", "Today")}</Th>
										<Th isNumeric>{tt("tunnelUsage.last7d", "Last 7 days")}</Th>
										<Th isNumeric>
											{tt("tunnelUsage.last30d", "Last 30 days")}
										</Th>
										<Th isNumeric>
											{tt("tunnelUsage.peakAvgShort", "Peak speed")}
										</Th>
									</Tr>
								</Thead>
								<Tbody>
									{data?.tunnels.map((item) => (
										<Tr
											key={item.tag}
											cursor="pointer"
											_hover={{ bg: "panel.elevated" }}
											onClick={() => setSelected(item.tag)}
										>
											<Td>
												<Text dir="ltr" textAlign="start" fontWeight="medium">
													{item.label}
												</Text>
												{item.countries.length > 0 && (
													<Text
														fontSize="10px"
														color="panel.textMuted"
														textTransform="capitalize"
														noOfLines={1}
													>
														{item.countries.join("، ")}
													</Text>
												)}
											</Td>
											<Td isNumeric dir="ltr">
												{formatBytes(sum(item.today), 2)}
											</Td>
											<Td isNumeric dir="ltr">
												{formatBytes(sum(item.last_7d), 2)}
											</Td>
											<Td isNumeric dir="ltr">
												{formatBytes(sum(item.last_30d), 2)}
											</Td>
											<Td isNumeric dir="ltr">
												{item.peak_hours.length
													? mbps(item.peak_avg_mbps)
													: "—"}
											</Td>
										</Tr>
									))}
								</Tbody>
							</Table>
						</Box>
					)}
				</VStack>
			)}
		</Box>
	);
};
