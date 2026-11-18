import {
	Alert,
	AlertDescription,
	AlertIcon,
	Box,
	Button,
	Divider,
	FormControl,
	FormHelperText,
	FormLabel,
	HStack,
	Input,
	SimpleGrid,
	Stack,
	Switch,
	Table,
	Tbody,
	Td,
	Text,
	Th,
	Thead,
	Tr,
	useToast,
} from "@chakra-ui/react";
import { PanelSelect as Select } from "components/common/PanelSelect";
import { type FC, useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { fetch as apiFetch } from "service/http";

type NordAutoLocation = {
	outbound: string;
	inbound?: string;
	port?: number;
	remark: string;
};

type NordAutoView = {
	enabled: boolean;
	target: string;
	address: string;
	sni: string;
	service_id: number;
	ai_outbound: string;
	locations: NordAutoLocation[];
	last_error?: string;
};

type ServiceOption = { id: number; name: string };

const shortTag = (tag: string) =>
	tag.replace(/^nord-/, "").replace(/\.nordvpn\.com$/, "");

/**
 * NordVPN auto configs: when on, every NordVPN location added as an outbound
 * gets its own config (through the entry server) on the chosen service, and
 * removing the location removes it. Separate from T.Sin.
 */
export const NordAutoPanel: FC = () => {
	const { t } = useTranslation();
	const toast = useToast();
	const [view, setView] = useState<NordAutoView | null>(null);
	const [services, setServices] = useState<ServiceOption[]>([]);
	const [draft, setDraft] = useState<Partial<NordAutoView>>({});
	const [saving, setSaving] = useState(false);

	const load = useCallback(async () => {
		try {
			const [next, list] = await Promise.all([
				apiFetch<NordAutoView>("/core/nord-auto"),
				apiFetch<{ services?: ServiceOption[] } | ServiceOption[]>(
					"/v2/services",
				),
			]);
			setView(next);
			setDraft({});
			const items = Array.isArray(list) ? list : (list.services ?? []);
			setServices(items.map((item) => ({ id: item.id, name: item.name })));
		} catch {
			setView(null);
		}
	}, []);

	useEffect(() => {
		void load();
	}, [load]);

	if (!view) return null;
	const value = { ...view, ...draft };

	const save = async (patch: Partial<NordAutoView>) => {
		setSaving(true);
		try {
			const next = await apiFetch<NordAutoView>("/core/nord-auto", {
				method: "PUT",
				body: patch,
			});
			setView(next);
			setDraft({});
			toast({
				title: t("pages.xray.nordAuto.saved", "NordVPN auto configs saved"),
				status: "success",
				isClosable: true,
				position: "top",
			});
		} catch (error: any) {
			toast({
				title:
					error?.data?.detail ||
					error?.response?._data?.detail ||
					t("pages.xray.nordAuto.saveFailed", "Could not save"),
				status: "error",
				isClosable: true,
				position: "top",
			});
		} finally {
			setSaving(false);
		}
	};

	return (
		<Stack spacing={3}>
			<Divider />
			<HStack justify="space-between" align="center">
				<Box>
					<Text fontWeight="semibold">
						{t("pages.xray.nordAuto.title", "Automatic configs")}
					</Text>
					<Text fontSize="sm" color="panel.textMuted">
						{t(
							"pages.xray.nordAuto.help",
							"Every location added here gets its own config on the chosen service after you save the settings page; removing a location removes its config.",
						)}
					</Text>
				</Box>
				<Switch
					isChecked={value.enabled}
					isDisabled={saving}
					onChange={(event) =>
						void save({ ...draft, enabled: event.target.checked })
					}
				/>
			</HStack>
			<SimpleGrid columns={{ base: 1, md: 2 }} spacing={3}>
				<FormControl>
					<FormLabel>
						{t("pages.xray.nordAuto.address", "Entry address")}
					</FormLabel>
					<Input
						dir="ltr"
						value={value.address}
						onChange={(event) =>
							setDraft({ ...draft, address: event.target.value })
						}
						placeholder="195.201.252.8"
					/>
					<FormHelperText>
						{t(
							"pages.xray.nordAuto.addressHelp",
							"The server users connect to (the Hetzner front).",
						)}
					</FormHelperText>
				</FormControl>
				<FormControl>
					<FormLabel>{t("pages.xray.nordAuto.service", "Service")}</FormLabel>
					<Select
						value={String(value.service_id || "")}
						onChange={(event) =>
							setDraft({
								...draft,
								service_id: Number(event.target.value) || 0,
							})
						}
					>
						<option value="">{t("select")}</option>
						{services.map((service) => (
							<option key={service.id} value={service.id}>
								{service.name}
							</option>
						))}
					</Select>
				</FormControl>
				<FormControl>
					<FormLabel>
						{t("pages.xray.nordAuto.ai", "AI sites leave through")}
					</FormLabel>
					<Select
						value={value.ai_outbound}
						onChange={(event) =>
							setDraft({ ...draft, ai_outbound: event.target.value })
						}
					>
						<option value="">
							{t("pages.xray.nordAuto.aiNone", "Each location itself")}
						</option>
						{value.locations.map((location) => (
							<option key={location.outbound} value={location.outbound}>
								{location.remark} ({shortTag(location.outbound)})
							</option>
						))}
					</Select>
				</FormControl>
				<FormControl>
					<FormLabel>REALITY SNI</FormLabel>
					<Input
						dir="ltr"
						value={value.sni}
						onChange={(event) =>
							setDraft({ ...draft, sni: event.target.value })
						}
					/>
				</FormControl>
			</SimpleGrid>
			<HStack justify="flex-end">
				<Button
					size="sm"
					colorScheme="primary"
					isLoading={saving}
					isDisabled={Object.keys(draft).length === 0}
					onClick={() => void save(draft)}
				>
					{t("save")}
				</Button>
			</HStack>
			{value.last_error ? (
				<Alert status="error" borderRadius="md">
					<AlertIcon />
					<AlertDescription fontSize="sm">{value.last_error}</AlertDescription>
				</Alert>
			) : null}
			{value.locations.length > 0 && (
				<Table size="sm">
					<Thead>
						<Tr>
							<Th>{t("pages.xray.nordAuto.location", "Location")}</Th>
							<Th>{t("pages.xray.nordAuto.config", "Config")}</Th>
						</Tr>
					</Thead>
					<Tbody>
						{value.locations.map((location) => (
							<Tr key={location.outbound}>
								<Td>
									{location.remark}{" "}
									<Text as="span" color="panel.textMuted" dir="ltr">
										({shortTag(location.outbound)})
									</Text>
								</Td>
								<Td dir="ltr">
									{location.inbound ? (
										`${location.inbound} : ${location.port ?? "?"}`
									) : (
										<Text as="span" color="panel.textMuted">
											{t(
												"pages.xray.nordAuto.pending",
												"made on the next save",
											)}
										</Text>
									)}
								</Td>
							</Tr>
						))}
					</Tbody>
				</Table>
			)}
		</Stack>
	);
};
