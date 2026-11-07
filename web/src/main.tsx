import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { RouterProvider, createBrowserRouter, Navigate, Outlet, useLocation } from "react-router-dom";

// Onest is the interface face (body, labels, inputs, buttons, terminal
// UI text), Archivo is headings/display text, IBM Plex Mono is every
// number and code -- all three self-hosted via Fontsource (OFL).
// Montserrat is retired (web light auth redesign brief, Part 1a).
import "@fontsource/onest/400.css";
import "@fontsource/onest/500.css";
import "@fontsource/onest/600.css";
import "@fontsource/onest/700.css";
import "@fontsource/archivo/600.css";
import "@fontsource/archivo/700.css";
import "@fontsource/ibm-plex-mono/400.css";
import "@fontsource/ibm-plex-mono/500.css";
import "@fontsource/ibm-plex-mono/600.css";
import "./styles/theme.css";

import { AppShell } from "@/components/layout/app-shell";
import { StyleGuide } from "@/routes/style-guide";
import { AnalysisPreview } from "@/routes/analysis-preview";
import { Setups } from "@/routes/setups";
import { Account } from "@/routes/account";
import { SignIn } from "@/routes/sign-in";
import { AuthProvider } from "@/auth/auth-context";
import { RequireAuth } from "@/auth/require-auth";
import { PublicOnly } from "@/auth/public-only";

// /login is now just a redirect to /sign-in (web light auth redesign
// brief, Part 1d) -- kept so an old bookmark or an in-flight email
// link still lands somewhere, preserving ?next= and any other query.
function LoginRedirect() {
  const location = useLocation();
  return <Navigate to={`/sign-in${location.search}`} replace />;
}

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
      { path: "login", element: <LoginRedirect /> },
      {
        element: <PublicOnly />,
        children: [{ path: "sign-in", element: <SignIn /> }],
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
