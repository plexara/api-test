import { create } from "zustand";
import { portalAPI, clearApiKey, setUnauthorizedHandler, type Identity } from "@/lib/api";

type Status = "idle" | "loading" | "authenticated" | "anonymous";

type AuthState = {
  identity: Identity | null;
  status: Status;
  refresh: () => Promise<void>;
  signOut: () => Promise<void>;
};

export const useAuth = create<AuthState>((set) => ({
  identity: null,
  status: "idle",
  refresh: async () => {
    set({ status: "loading" });
    try {
      const id = await portalAPI.me();
      set({ identity: id, status: "authenticated" });
    } catch {
      set({ identity: null, status: "anonymous" });
    }
  },
  signOut: async () => {
    clearApiKey();
    try {
      await fetch("/portal/auth/logout", {
        method: "POST",
        credentials: "include",
        headers: { "X-Requested-With": "XMLHttpRequest" },
      });
    } catch { /* ignore */ }
    set({ identity: null, status: "anonymous" });
    window.location.href = "/portal/login";
  },
}));

setUnauthorizedHandler(() => {
  clearApiKey();
  useAuth.setState({ identity: null, status: "anonymous" });
  if (typeof window !== "undefined" && !window.location.pathname.endsWith("/login")) {
    window.location.href = "/portal/login";
  }
});
