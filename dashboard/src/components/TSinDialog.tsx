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
	Text,
	useToast,
} from "@chakra-ui/react";
import { PanelSelect as Select } from "components/common/PanelSelect";
import { AppDialog } from "components/dialogs/AppDialog";
import { ConfirmDialog } from "components/dialogs/ConfirmDialog";
import { Input } from "components/Input";
import { type FC, useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { fetch } from "service/http";

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

type TSinView = {
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
};

type TSinApplyResponse = {
	service_name: string;
	tunnel_up: boolean;
	warning?: string;
	inbounds: { tag: string; port: number; country: string }[];
};

const formatGB = (value: number) => `${value.toFixed(2)} GB`;

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
	const [selected, setSelected] = useState<string[]>([]);
	const [result, setResult] = useState<TSinApplyResponse | null>(null);
	const [confirmRemove, setConfirmRemove] = useState(false);

	// biome-ignore lint/correctness/useExhaustiveDependencies: the dialog loads once each time it opens, not whenever the toast or translator changes identity
	const load = useCallback(async () => {
		setLoading(true);
		try {
			const data = await fetch<TSinView>("/core/tsin");
			setView(data);
			setDc(data.dc);
			setSelected(
				data.countries?.length ? data.countries : (data.preferred ?? []),
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
			const body: Record<string, unknown> = { dc, countries: selected };
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
			toast({
				status: "error",
				isClosable: true,
				title:
					error?.data?.detail ?? tt("tsin.failed", "Could not apply T.Sin"),
			});
		} finally {
			setBusy(false);
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

					<Alert status="info" borderRadius="6px" fontSize="sm">
						<AlertIcon />
						<AlertDescription>
							{tt(
								"tsin.scope",
								"This works on the server the panel itself runs on. Other nodes keep their normal configs.",
							)}
						</AlertDescription>
					</Alert>

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
						<Select value={dc} onChange={(event) => setDc(event.target.value)}>
							{(view?.datacenters ?? ["dc1"]).map((item) => (
								<option key={item} value={item}>
									{item}
								</option>
							))}
						</Select>
					</FormControl>

					{view?.account_error && (
						<Alert status="warning" borderRadius="6px" fontSize="sm">
							<AlertIcon />
							<AlertDescription dir="ltr">
								{view.account_error}
							</AlertDescription>
						</Alert>
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

					{view?.applied && (
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
