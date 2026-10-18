import { create } from 'zustand';
import type { User } from '@/types';
import { getMe } from '@/api/auth';

interface AuthState {
  user: User | null;
  isAdmin: boolean;
  pendingLinkRequests: number;
  /** Whether the server offers barcode lookup (enable_barcode). */
  barcodeEnabled: boolean;
  isLoading: boolean;
  isInitialized: boolean;
  fetchUser: () => Promise<void>;
  setUser: (user: User | null, isAdmin?: boolean) => void;
  clearUser: () => void;
}

export const useAuthStore = create<AuthState>((set, get) => ({
  user: null,
  isAdmin: false,
  pendingLinkRequests: 0,
  barcodeEnabled: false,
  isLoading: true,
  isInitialized: false,

  fetchUser: async () => {
    // Only surface the loading state on the initial fetch. Background
    // refreshes (e.g. Settings' onSave → refresh) must not flip pages into
    // their loading gate — that unmounts the whole tree and wipes local UI
    // state (2FA backup codes reveal, in-flight form values).
    if (!get().user) set({ isLoading: true });
    try {
      const data = await getMe();
      set({ user: data.user, isAdmin: data.isAdmin, pendingLinkRequests: data.pendingLinkRequests || 0, barcodeEnabled: !!data.barcodeEnabled, isLoading: false, isInitialized: true });
    } catch {
      set({ user: null, isAdmin: false, pendingLinkRequests: 0, barcodeEnabled: false, isLoading: false, isInitialized: true });
    }
  },

  setUser: (user, isAdmin = false) => set({ user, isAdmin, isLoading: false, isInitialized: true }),
  clearUser: () => set({ user: null, isAdmin: false, pendingLinkRequests: 0, barcodeEnabled: false, isLoading: false }),
}));
