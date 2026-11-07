import { Navigate, Outlet } from "react-router-dom";
import { AuthLayout } from "@/components/layout/auth-layout";
import { useAuth } from "./auth-context";
import { AuthLoadingShell } from "./auth-loading-shell";

// Guards /login, /register, /verify-email (web auth UI brief, Part
// A2): a signed-in user visiting any of them is redirected to /setups
// -- checked before rendering the layout, so there is no flash of the
// login form first.
export function PublicOnly() {
  const { status } = useAuth();

  if (status === "loading") return <AuthLoadingShell />;
  if (status === "signedIn") return <Navigate to="/setups" replace />;
  return (
    <AuthLayout>
      <Outlet />
    </AuthLayout>
  );
}
