import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider, createBrowserRouter, Navigate, Outlet } from "react-router-dom";

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
import { Account } from "@/routes/account";
import { Login } from "@/routes/login";
import { Register } from "@/routes/register";
import { VerifyEmail } from "@/routes/verify-email";
import { AuthProvider } from "@/auth/auth-context";
import { RequireAuth } from "@/auth/require-auth";
import { PublicOnly } from "@/auth/public-only";

// AuthProvider must be a router descendant (it calls useNavigate/
// useLocation for the global 401 handler -- web auth UI brief, Part
// A2), so it wraps both the public and protected branches below via
// this root element, rather than wrapping <RouterProvider> itself.
const router = createBrowserRouter([
  {
    element: (
      <AuthProvider>
        <Outlet />
      </AuthProvider>
    ),
    children: [
      {
        element: <PublicOnly />,
        children: [
          { path: "login", element: <Login /> },
          { path: "register", element: <Register /> },
          { path: "verify-email", element: <VerifyEmail /> },
        ],
      },
      {
        element: <RequireAuth />,
        children: [
          {
            element: <AppShell />,
            children: [
              { index: true, element: <Navigate to="/setups" replace /> },
              { path: "analysis", element: <AnalysisPreview /> },
              { path: "setups", element: <Setups /> },
              { path: "account", element: <Account /> },
              { path: "style", element: <StyleGuide /> },
            ],
          },
        ],
      },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <RouterProvider router={router} />
  </StrictMode>,
);
