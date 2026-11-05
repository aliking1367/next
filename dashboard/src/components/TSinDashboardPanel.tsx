import { useDisclosure } from "@chakra-ui/react";
import type { FC } from "react";
import { useQueryClient } from "react-query";
import { TSinDialog } from "./TSinDialog";
import { TSinStatusCard, tsinStatusQueryKey } from "./TSinStatusCard";

// TSinDashboardPanel puts the T.Sin card on the dashboard, the first page an
// admin sees, as a tile among the system cards, with the Countries dialog it
// opens. It shows nothing on a panel without T.Sin.
export const TSinDashboardPanel: FC = () => {
	const dialog = useDisclosure();
	const queryClient = useQueryClient();
	return (
		<>
			<TSinStatusCard onOpen={dialog.onOpen} tile />
			<TSinDialog
				isOpen={dialog.isOpen}
				onClose={dialog.onClose}
				onChanged={() =>
					void queryClient.invalidateQueries([tsinStatusQueryKey])
				}
			/>
		</>
	);
};
