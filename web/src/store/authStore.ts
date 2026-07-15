import { create } from 'zustand';
import { persist } from 'zustand/middleware';

interface User {
  id: string;
  email: string;
  name: string;
  tenant_id: string;
  role: number;
  twoFactorEnabled?: boolean;
}

interface AuthState {
  user: User | null;
  token: string | null;
  selectedTenantID: string | null;
  isAuthenticated: boolean;
  setAuth: (user: User, token: string) => void;
  setSelectedTenantID: (id: string | null) => void;
  clearAuth: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      user: null,
      token: null,
      selectedTenantID: null,
      isAuthenticated: false,
      setAuth: (user, token) => set({ user, token, isAuthenticated: true, selectedTenantID: user.tenant_id }),
      setSelectedTenantID: (id) => set({ selectedTenantID: id }),
      clearAuth: () => set({ user: null, token: null, isAuthenticated: false, selectedTenantID: null }),
    }),
    {
      name: 'panmail-auth',
    }
  )
);
