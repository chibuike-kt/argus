import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider, createBrowserRouter, Navigate } from "react-router-dom";

// Poppins is the interface face, self-hosted via Fontsource (OFL).
import "@fontsource/poppins/300.css";
import "@fontsource/poppins/400.css";
import "@fontsource/poppins/500.css";
import "@fontsource/poppins/600.css";
import "@fontsource/poppins/700.css";
import "./styles/theme.css";

import { AppShell } from "@/components/layout/app-shell";
import { StyleGuide } from "@/routes/style-guide";
import { AnalysisPreview } from "@/routes/analysis-preview";
import { Setups } from "@/routes/setups";

const router = createBrowserRouter([
  {
    element: <AppShell />,
    children: [
      { index: true, element: <Navigate to="/analysis" replace /> },
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
