import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider, createBrowserRouter, Navigate } from "react-router-dom";

// IBM Plex Sans/Mono are the interface faces, self-hosted via Fontsource (OFL).
import "@fontsource/ibm-plex-sans/300.css";
import "@fontsource/ibm-plex-sans/400.css";
import "@fontsource/ibm-plex-sans/500.css";
import "@fontsource/ibm-plex-sans/600.css";
import "@fontsource/ibm-plex-sans/700.css";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
import "@fontsource/ibm-plex-mono/600.css";
import "./styles/theme.css";

import { AppShell } from "@/components/layout/app-shell";
import { StyleGuide } from "@/routes/style-guide";
import { AnalysisPreview } from "@/routes/analysis-preview";
import { Setups } from "@/routes/setups";

const router = createBrowserRouter([
  {
    element: <AppShell />,
    children: [
      { index: true, element: <Navigate to="/setups" replace /> },
      { path: "analysis", element: <AnalysisPreview /> },
      { path: "setups", element: <Setups /> },
      { path: "style", element: <StyleGuide /> },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
);
