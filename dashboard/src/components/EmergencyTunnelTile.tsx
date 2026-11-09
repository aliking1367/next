import { Box, HStack, Switch, Text, useToast } from "@chakra-ui/react";
import type { FC } from "react";
import { useTranslation } from "react-i18next";
import { useMutation, useQuery, useQueryClient } from "react-query";
import { fetch } from "service/http";

type EmergencyTunnelState = {
	allowed: boolean;
	configured: boolean;
	scope: "all" | "mine";
	enabled: boolean;
	global_enabled: boolean;
	admins_on?: number;
};

const queryKey = ["emergency-tunnel"];

// EmergencyTunnelTile is the dashboard's switch for the emergency tunnel,
// for the hours the countries' exits are down. Admins see it, so it never
// names the provider. The owner turns it on for every
// user of the countries; an admin given the permission, for their own users.
// It shows only to those who may use it, once the owner has set it up.
export const EmergencyTunnelTile: FC = () => {
	const { t } = useTranslation();
	const tt = (
		key: string,
		fallback: string,
		options?: Record<string, unknown>,
	) => t(key, { defaultValue: fallback, ...options }) as string;
	const toast = useToast();
	const queryClient = useQueryClient();
	const { data } = useQuery(
		queryKey,
		() => fetch<EmergencyTunnelState>("/emergency-tunnel"),
		{ refetchInterval: 60_000, refetchOnWindowFocus: false, retry: false },
	);
	const mutation = useMutation(
		(enabled: boolean) =>
			fetch<EmergencyTunnelState>("/emergency-tunnel", {
				method: "POST",
				body: { enabled },
			}),
		{
			onSuccess: (result) => {
				queryClient.setQueryData(queryKey, result);
				toast({
					status: result.enabled ? "warning" : "success",
					isClosable: true,
					duration: 10_000,
					title: result.enabled
						? tt(
								"emergency.tile.on",
								"Emergency tunnel is on. Users update their subscription and connect to the 🆘 config.",
							)
						: tt(
								"emergency.tile.off",
								"Emergency tunnel is off. The 🆘 config stops working right away.",
							),
				});
			},
			onError: (error: any) => {
				toast({
					status: "error",
					isClosable: true,
					title: error?.data?.detail ?? String(error),
				});
			},
		},
	);

	if (!data?.allowed || !data.configured) return null;
	const on = data.enabled;
	const color = on ? "red.400" : "panel.textMuted";

	return (
		<Box
			h="full"
			minH="170px"
			display="flex"
			flexDirection="column"
			borderWidth="1px"
			borderColor={on ? "red.400" : "panel.border"}
			borderRadius="xl"
			bg="panel.surface"
			p={4}
			position="relative"
			overflow="hidden"
		>
			<Box
				position="absolute"
				insetInlineStart={0}
				top={0}
				bottom={0}
				w="3px"
				bg={on ? "red.400" : "panel.border"}
			/>
			<HStack justify="space-between" spacing={2}>
				<Text fontSize="sm" fontWeight="semibold" noOfLines={1}>
					{tt("emergency.tile.title", "🆘 Emergency tunnel")}
				</Text>
				<Switch
					colorScheme="red"
					size="lg"
					isChecked={on}
					isDisabled={mutation.isLoading}
					onChange={(event) => mutation.mutate(event.target.checked)}
				/>
			</HStack>

			<Box flex="1" py={2.5}>
				<Text fontSize="xl" fontWeight="bold" color={color}>
					{on
						? tt("emergency.tile.isOn", "On")
						: tt("emergency.tile.isOff", "Off")}
				</Text>
				<Text fontSize="xs" color="panel.textMuted" mt={1}>
					{data.scope === "all"
						? tt("emergency.tile.scopeAll", "For every user of the countries")
						: tt("emergency.tile.scopeMine", "For your own users")}
				</Text>
			</Box>

			<Text
				fontSize="xs"
				color="panel.textMuted"
				pt={2}
				borderTopWidth="1px"
				borderColor="panel.border"
			>
				{data.scope === "mine" && data.global_enabled
					? tt(
							"emergency.tile.globalOn",
							"The panel owner has it on for everyone already.",
						)
					: data.scope === "all" && (data.admins_on ?? 0) > 0
						? tt(
								"emergency.tile.adminsOn",
								"On for the users of {{count}} admin(s). Turning yours off turns theirs off too.",
								{ count: data.admins_on },
							)
						: tt(
								"emergency.tile.hint",
								"Turn it on only while the countries are down; its users go out from the server directly.",
							)}
			</Text>
		</Box>
	);
};
