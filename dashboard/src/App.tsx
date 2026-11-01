import "react-datepicker/dist/react-datepicker.css";
import { SeasonalOverlay } from "components/SeasonalOverlay";
import { SeasonalProvider } from "contexts/SeasonalContext";
import { useSheetDrag } from "hooks/useSheetDrag";
import { RouterProvider } from "react-router-dom";
import { router } from "./pages/Router";

function App() {
	useSheetDrag();
	return (
		<SeasonalProvider>
			<SeasonalOverlay />
			<RouterProvider router={router} />
		</SeasonalProvider>
	);
}

export default App;
