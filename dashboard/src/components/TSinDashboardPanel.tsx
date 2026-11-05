import { useDisclosure } from "@chakra-ui/react";
import type { FC } from "react";
import { useQueryClient } from "react-query";
import { TSinDialog } from "./TSinDialog";
import { TSinStatusCard, tsinStatusQueryKey } from "./TSinStatusCard";

// TSinDashboardPanel puts the T.Sin card on the dashboard, the first page an
// admin sees, with the Countries dialog it opens. It shows nothing on a panel
// without T.Sin.
export const TSinDashboardPanel: FC = () => {
	const dialog = useDisclosure();
	const queryClient = useQueryClient();
	return (
		<>
			<TSinStatusCard onOpen={dialog.onOpen} wide />
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
