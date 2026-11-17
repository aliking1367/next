import { Box, chakra, Flex, Grid, Text, Tooltip } from "@chakra-ui/react";
import type { FC, ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { formatBytes } from "utils/formatByte";

export type OverviewStatusKey =
	| "active"
	| "expired"
	| "limited"
	| "on_hold"
	| "disabled";

const STATUS_ORDER: OverviewStatusKey[] = [
	"active",
	"expired",
	"limited",
	"on_hold",
	"disabled",
];

// The same colours the old summary pills used for each status.
const STATUS_COLOR: Record<OverviewStatusKey, string> = {
	active: "green.400",
	expired: "red.400",
	limited: "yellow.400",
	on_hold: "orange.400",
	disabled: "gray.500",
};

type UsersOverviewBandProps = {
	total: number;
	usersLimit?: number | null;
	active: number;
	online: number;
	usage: number | null;
	usageLabel: string;
	usageHelper?: string;
	breakdown: Partial<Record<string, number>>;
	locale: string;
	limitReached?: boolean;
	selectedStatus?: OverviewStatusKey;
	onSelectStatus: (status?: OverviewStatusKey) => void;
};

const count = (value: number | null | undefined, locale: string) =>
	value === null || value === undefined
		? "—"
		: new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(value);

const Label: FC<{ children: ReactNode }> = ({ children }) => (
	<Text
		fontSize="11px"
		fontWeight="medium"
		color="panel.textMuted"
		lineHeight="18px"
	>
		{children}
	</Text>
);

const Dot: FC<{ color: string }> = ({ color }) => (
	<Box
		as="span"
		w="6px"
		h="6px"
		borderRadius="full"
		bg={color}
		flexShrink={0}
	/>
);

// A clickable figure: picks a status filter, and shows when it is the one in use.
const Pick: FC<{
	label: string;
	isActive?: boolean;
	onClick?: () => void;
	children: ReactNode;
}> = ({ label, isActive, onClick, children }) =>
	onClick ? (
		<chakra.button
			type="button"
			aria-label={label}
			aria-pressed={Boolean(isActive)}
			title={label}
			onClick={onClick}
			display="block"
			textAlign="start"
			w="full"
			borderRadius="sm"
			color={isActive ? "panel.accent" : undefined}
			transition="color 0.15s ease"
			_hover={{ color: "panel.accent" }}
			_focusVisible={{ outline: "2px solid", outlineColor: "panel.accent" }}
		>
			{children}
		</chakra.button>
	) : (
		<Box w="full" title={label}>
			{children}
		</Box>
	);

// Each part of the band sits in its own framed tile, like the table rows.
const tile = {
	borderWidth: "1px",
	borderColor: "panel.border",
	borderRadius: "xl",
	bg: "panel.main",
	px: 4,
	py: 3,
	transition: "border-color 0.2s ease",
	_hover: { borderColor: "panel.borderStrong" },
};

const LiveDot: FC = () => (
	<Box as="span" position="relative" w="8px" h="8px" flexShrink={0}>
		<Box
			as="span"
			position="absolute"
			inset={0}
			borderRadius="full"
			bg="panel.accent"
			opacity={0.6}
			sx={{
				animation: "rb-live-ping 1.8s cubic-bezier(0, 0, 0.2, 1) infinite",
				"@keyframes rb-live-ping": {
					"75%, 100%": { transform: "scale(2.2)", opacity: 0 },
				},
				"@media (prefers-reduced-motion: reduce)": { animation: "none" },
			}}
		/>
		<Box
			as="span"
			position="absolute"
			inset="1px"
			borderRadius="full"
			bg="panel.accent"
		/>
	</Box>
);

/**
 * The users overview at the top of the list, after upstream Rebecca's band:
 * the total against the admin's cap, online and active with their share, the
 * spread of users across statuses, and the list's traffic with a per-user
 * average. Clicking a status filters the list by it; clicking again clears it.
 */
export const UsersOverviewBand: FC<UsersOverviewBandProps> = ({
	total,
	usersLimit,
	active,
	online,
	usage,
	usageLabel,
	usageHelper,
	breakdown,
	locale,
	limitReached,
	selectedStatus,
	onSelectStatus,
}) => {
	const { t } = useTranslation();
	const totals = STATUS_ORDER.reduce(
		(sum, key) => sum + (breakdown[key] ?? 0),
		0,
	);
	const base = totals || total || 1;
	const share = (value: number) => Math.round((value / base) * 100);
	const limitPercent = usersLimit
		? Math.min(100, Math.round((total / usersLimit) * 100))
		: null;
	const toggle = (status: OverviewStatusKey) =>
		onSelectStatus(selectedStatus === status ? undefined : status);

	return (
		<Grid
			className="rb-users-overview"
			aria-label={t("usersStatusBreakdown")}
			templateColumns={{
				base: "repeat(2, minmax(0, 1fr))",
				xl: "minmax(0, 13rem) minmax(0, 0.8fr) minmax(0, 1.3fr) minmax(0, 13rem)",
			}}
			gap={3}
			w="full"
		>
			{/* Total */}
			<Flex
				direction="column"
				justify="space-between"
				gap={2}
				order={1}
				{...tile}
			>
				<Label>{t("usersTable.total")}</Label>
				<Box>
					<Flex align="baseline" gap={2}>
						<Pick
							label={t("usersTable.total")}
							isActive={false}
							onClick={
								selectedStatus ? () => onSelectStatus(undefined) : undefined
							}
						>
							<Text
								as="span"
								fontSize={{ base: "22px", xl: "28px" }}
								whiteSpace="nowrap"
								fontWeight="semibold"
								lineHeight="1.1"
								color={limitReached ? "red.400" : "panel.text"}
								sx={{ fontVariantNumeric: "tabular-nums" }}
							>
								{count(total, locale)}
							</Text>
						</Pick>
						{usersLimit ? (
							<Text fontSize="xs" color="panel.textMuted" whiteSpace="nowrap">
								/ {count(usersLimit, locale)}
							</Text>
						) : null}
					</Flex>
					{limitPercent !== null ? (
						<Box mt={2}>
							<Box
								h="4px"
								borderRadius="full"
								bg="panel.border"
								overflow="hidden"
							>
								<Box
									h="full"
									borderRadius="full"
									bg={limitReached ? "red.400" : "panel.accent"}
									w={`${limitPercent}%`}
									transition="width 0.5s ease"
								/>
							</Box>
							<Text mt={1} fontSize="10px" color="panel.textMuted">
								{count(limitPercent, locale)}%
							</Text>
						</Box>
					) : (
						<Text mt={2} fontSize="10px" color="panel.textMuted">
							{t("users")}
						</Text>
					)}
				</Box>
			</Flex>

			{/* Online and active */}
			<Grid
				order={{ base: 3, xl: 2 }}
				gridColumn={{ base: "span 2", xl: "auto" }}
				templateColumns={{ base: "repeat(2, minmax(0, 1fr))", xl: "1fr" }}
				alignContent="space-between"
				gap={3}
				{...tile}
			>
				{[
					{
						key: "online",
						label: t("onlineStatus.online"),
						value: online,
						color: "panel.accent",
					},
					{
						key: "active",
						label: t("status.active"),
						value: active,
						color: "green.400",
					},
				].map((row) => (
					<Pick
						key={row.key}
						label={row.label}
						isActive={row.key === "active" && selectedStatus === "active"}
						onClick={row.key === "active" ? () => toggle("active") : undefined}
					>
						<Flex align="center" gap={1.5}>
							{row.key === "online" ? <LiveDot /> : <Dot color={row.color} />}
							<Label>{row.label}</Label>
						</Flex>
						<Flex align="baseline" gap={2} mt={0.5}>
							<Text
								as="span"
								fontSize="lg"
								fontWeight="semibold"
								lineHeight="1.2"
								color="inherit"
								sx={{ fontVariantNumeric: "tabular-nums" }}
							>
								{count(row.value, locale)}
							</Text>
							<Text as="span" fontSize="10px" color="panel.textMuted">
								{count(share(row.value), locale)}%
							</Text>
						</Flex>
					</Pick>
				))}
			</Grid>

			{/* Spread across statuses */}
			<Flex
				order={{ base: 4, xl: 3 }}
				gridColumn={{ base: "span 2", xl: "auto" }}
				direction="column"
				justify="center"
				gap={2}
				{...tile}
			>
				<Flex align="center" justify="space-between">
					<Label>{t("usersStatusBreakdown")}</Label>
					<Text fontSize="11px" color="panel.textMuted">
						{count(totals, locale)}
					</Text>
				</Flex>
				<Flex
					h="6px"
					w="full"
					gap="1px"
					borderRadius="full"
					overflow="hidden"
					bg="panel.border"
				>
					{STATUS_ORDER.map((key) => {
						const value = breakdown[key] ?? 0;
						if (!value) return null;
						return (
							<Tooltip
								key={key}
								label={`${t(`status.${key}`)}: ${count(value, locale)}`}
								hasArrow
							>
								<Box
									h="full"
									bg={STATUS_COLOR[key]}
									w={`${(value / (totals || 1)) * 100}%`}
								/>
							</Tooltip>
						);
					})}
				</Flex>
				<Grid
					templateColumns="repeat(2, minmax(0, 1fr))"
					columnGap={6}
					rowGap={1}
				>
					{STATUS_ORDER.map((key) => (
						<Pick
							key={key}
							label={t(`status.${key}`)}
							isActive={selectedStatus === key}
							onClick={() => toggle(key)}
						>
							<Flex align="center" gap={1.5} fontSize="11px" lineHeight="20px">
								<Dot color={STATUS_COLOR[key]} />
								<Text
									as="span"
									color={
										selectedStatus === key ? "inherit" : "panel.textSecondary"
									}
								>
									{t(`status.${key}`)}
								</Text>
								<Text
									as="span"
									ms="auto"
									fontWeight="semibold"
									color={selectedStatus === key ? "inherit" : "panel.text"}
									sx={{ fontVariantNumeric: "tabular-nums" }}
								>
									{count(breakdown[key] ?? 0, locale)}
								</Text>
							</Flex>
						</Pick>
					))}
				</Grid>
			</Flex>

			{/* The list's traffic */}
			<Flex
				order={{ base: 2, xl: 4 }}
				direction="column"
				justify="space-between"
				gap={2}
				{...tile}
			>
				<Tooltip
					label={usageHelper}
					isDisabled={!usageHelper}
					hasArrow
					placement="top"
				>
					<Box>
						<Label>{usageLabel}</Label>
					</Box>
				</Tooltip>
				{usage !== null ? (
					<Box>
						<Text
							fontSize={{ base: "22px", xl: "28px" }}
							whiteSpace="nowrap"
							fontWeight="semibold"
							lineHeight="1.1"
							color="panel.text"
							dir="ltr"
							textAlign="start"
							sx={{
								unicodeBidi: "isolate",
								fontVariantNumeric: "tabular-nums",
							}}
						>
							{formatBytes(usage)}
						</Text>
						<Flex mt={2} gap={1} fontSize="10px" color="panel.textMuted">
							{total > 0 ? (
								<>
									<Text as="span" dir="ltr" sx={{ unicodeBidi: "isolate" }}>
										{formatBytes(Math.round(usage / Math.max(1, total)))}
									</Text>
									<Text as="span">/ {t("user")}</Text>
								</>
							) : null}
						</Flex>
					</Box>
				) : (
					<Text
						fontSize={{ base: "22px", xl: "28px" }}
						whiteSpace="nowrap"
						fontWeight="semibold"
						color="panel.textMuted"
					>
						—
					</Text>
				)}
			</Flex>
		</Grid>
	);
};
