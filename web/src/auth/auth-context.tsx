import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useNavigate, useLocation } from "react-router-dom";
import * as authApi from "@/api/auth";
import { setUnauthorizedHandler } from "@/api/client";
import type { ApiAuthUser } from "@/api/types";
import { fixturesEnabled } from "@/mocks/fixtures-as-api";

export type AuthStatus = "loading" | "signedIn" | "signedOut";

export interface AuthContextValue {
  status: AuthStatus;
  user: ApiAuthUser | null;
  signIn: (email: string, password: string) => Promise<ApiAuthUser>;
  signOut: () => Promise<void>;
  signOutAll: () => Promise<void>;
  refresh: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

// A fixed fixture user (web auth UI brief, Part A2): VITE_USE_FIXTURES
// makes no network call at all and is always "signed in", so offline
// dev and the style guide keep working exactly as before this brief.
const FIXTURE_USER: ApiAuthUser = {
  id: 0,
  email: "sample@argus.local",
  email_verified: true,
  created_at: new Date(0).toISOString(),
};

export function AuthProvider({ children }: { children: ReactNode }) {
  const usingFixtures = fixturesEnabled();
  const [status, setStatus] = useState<AuthStatus>(usingFixtures ? "signedIn" : "loading");
  const [user, setUser] = useState<ApiAuthUser | null>(usingFixtures ? FIXTURE_USER : null);
  const navigate = useNavigate();
  const location = useLocation();
  // Read inside the 401 handler below without re-registering it (and
  // so re-running the boot /auth/me call) on every navigation.
  const locationRef = useRef(location);
  locationRef.current = location;

  useEffect(() => {
    if (usingFixtures) return;
    let cancelled = false;

    authApi
      .me()
      .then((u) => {
        if (cancelled) return;
        setUser(u);
        setStatus("signedIn");
      })
      .catch(() => {
        if (cancelled) return;
        setUser(null);
        setStatus("signedOut");
      })
      .finally(() => {
        if (cancelled) return;
        // Registered only after the boot check resolves, so the
        // global handler never fires for that initial "are we signed
        // in at all" probe -- only for a 401 that arrives afterwards,
        // meaning a previously-live session just died.
        setUnauthorizedHandler(() => {
          setUser(null);
          setStatus("signedOut");
          const current = locationRef.current;
          navigate(`/sign-in?next=${encodeURIComponent(current.pathname + current.search)}`, {
            replace: true,
          });
        });
      });

    return () => {
      cancelled = true;
      setUnauthorizedHandler(null);
    };
    // Intentionally runs once (plus if fixtures mode itself somehow
    // changed): re-reading navigate/location on every render would
    // tear down and rebuild the handler (and re-run the boot call) on
    // every navigation, which is exactly what locationRef above exists
    // to avoid.
  }, [usingFixtures]);

  const signIn = async (email: string, password: string) => {
    const u = await authApi.login(email, password);
    setUser(u);
    setStatus("signedIn");
    return u;
  };

  const signOut = async () => {
    try {
      await authApi.logout();
    } finally {
      setUser(null);
      setStatus("signedOut");
    }
  };

  const signOutAll = async () => {
    try {
      await authApi.logoutAll();
    } finally {
      setUser(null);
      setStatus("signedOut");
    }
  };

  const refresh = async () => {
    try {
      const u = await authApi.me();
      setUser(u);
      setStatus("signedIn");
    } catch {
      setUser(null);
      setStatus("signedOut");
    }
  };

  return (
    <AuthContext.Provider value={{ status, user, signIn, signOut, signOutAll, refresh }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within an AuthProvider");
  return ctx;
}
