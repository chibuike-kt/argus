import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider, createBrowserRouter, Navigate } from "react-router-dom";

// Montserrat is the interface face, IBM Plex Mono is every number -- both
// self-hosted via Fontsource (OFL).
import "@fontsource/montserrat/400.css";
import "@fontsource/montserrat/500.css";
import "@fontsource/montserrat/600.css";
import "@fontsource/montserrat/700.css";
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
