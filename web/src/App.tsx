import { MantineProvider, createTheme } from '@mantine/core';
import { Notifications } from '@mantine/notifications';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider, createRouter, createRoute, createRootRoute, Outlet, redirect } from '@tanstack/react-router';
import { EmailProvidersPage } from './pages/EmailProvidersPage';
import { SignInPage } from './pages/SignInPage';
import { SetupPage } from './pages/SetupPage';
import { LogsPage } from './pages/LogsPage';
import { ProfilePage } from './pages/ProfilePage';
import { TemplatesPage } from './pages/TemplatesPage';
import { TemplateEditorPage } from './pages/TemplateEditorPage';
import { DashboardPage } from './pages/DashboardPage';
import { AnalyticsPage } from './pages/AnalyticsPage';
import { SuppressionsPage } from './pages/SuppressionsPage';
import { WebhooksPage } from './pages/WebhooksPage';
import { InboundEmailsPage } from './pages/InboundEmailsPage';
import { ApiKeysPage } from './pages/ApiKeysPage';
import { UsersPage } from './pages/UsersPage';
import { TenantsPage } from './pages/TenantsPage';
import { ArchivesPage } from './pages/ArchivesPage';
import { SettingsPage } from './pages/SettingsPage';
import { TestDeliveryPage } from './pages/TestDeliveryPage';
import { AppLayout } from './layouts/AppLayout';
import { useAuthStore } from './store/authStore';
import { setupClient } from './services/client';
import '@mantine/core/styles.css';
import '@mantine/notifications/styles.css';
import '@mantine/charts/styles.css';
import '@mantine/dates/styles.css';

const queryClient = new QueryClient();

const rootRoute = createRootRoute({
  component: () => (
    <>
      <Outlet />
      <Notifications />
    </>
  ),
  beforeLoad: async ({ location }) => {
    // Check setup status
    try {
      const { isSetup } = await setupClient.getSetupStatus({});
      if (!isSetup && location.pathname !== '/setup') {
        throw redirect({ to: '/setup' });
      }
      if (isSetup && location.pathname === '/setup') {
        throw redirect({ to: '/' });
      }
    } catch (e) {
      if (e instanceof Error && e.message.includes('redirect')) throw e;
      // If error checking setup, we might be offline or server down.
    }
  },
});

const appLayoutRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'app',
  component: AppLayout,
  beforeLoad: () => {
    const { isAuthenticated } = useAuthStore.getState();
    if (!isAuthenticated) {
      throw redirect({ to: '/signin' });
    }
  },
});

const indexRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/',
  component: EmailProvidersPage,
});

const logsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/logs',
  component: LogsPage,
});

const templatesRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/templates',
  component: TemplatesPage,
});

const templateNewRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/templates/new',
  component: TemplateEditorPage,
});

const templateEditRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/templates/$id/edit',
  component: TemplateEditorPage,
});

const dashboardRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/dashboard',
  component: DashboardPage,
});

const analyticsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/analytics',
  component: AnalyticsPage,
});

const suppressionsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/suppressions',
  component: SuppressionsPage,
});

const inboundRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/inbound',
  component: InboundEmailsPage,
});

const webhooksRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/webhooks',
  component: WebhooksPage,
});

const profileRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/profile',
  component: ProfilePage,
});

const apiKeysRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/api-keys',
  component: ApiKeysPage,
});

const usersRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/users',
  component: UsersPage,
});

const tenantsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/tenants',
  component: TenantsPage,
});

const settingsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/settings',
  component: SettingsPage,
});

const signInRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/signin',
  component: SignInPage,
});

const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/setup',
  component: SetupPage,
});

const archivesRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/archives',
  component: ArchivesPage,
});

const testDeliveryRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: '/test-delivery',
  component: TestDeliveryPage,
});

const routeTree = rootRoute.addChildren([
  appLayoutRoute.addChildren([
    indexRoute, 
    dashboardRoute, 
    templatesRoute, 
    templateNewRoute,
    templateEditRoute,
    logsRoute, 
    analyticsRoute, 
    archivesRoute,
    testDeliveryRoute,
    suppressionsRoute, 
    inboundRoute, 
    apiKeysRoute, 
    usersRoute, 
    tenantsRoute, 
    webhooksRoute, 
    settingsRoute,
    profileRoute
  ]),
  signInRoute,
  setupRoute,
]);

const router = createRouter({ routeTree });

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}

const theme = createTheme({
  primaryColor: 'brand',
  primaryShade: { light: 6, dark: 6 },
  fontFamily: 'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif',
  headings: {
    fontFamily: 'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif',
    fontWeight: '800',
  },
  defaultRadius: 'md',
  white: '#ffffff',
  black: '#111111',
  colors: {
    // Custom blue shades inspired by monday.com
    brand: [
      '#e5f4ff',
      '#d1e9ff',
      '#a3d2ff',
      '#75bbff',
      '#47a4ff',
      '#198dff',
      '#0073ea', // Primary blue
      '#005bb8',
      '#004385',
      '#002b52',
    ],
    // High-contrast dark theme palette (blue-based dark)
    dark: [
      '#ffffff', // 0: primary text
      '#c5c7d0', // 1: secondary text
      '#9ca3af', // 2: placeholder/dimmed
      '#4d4f66', // 3: borders
      '#34354a', // 4: lighter surface
      '#2b2c3d', // 5: surface
      '#1d1f27', // 6: card background
      '#11121d', // 7: main background
      '#0c0d14', // 8: deep background
      '#050505', // 9: deepest
    ],
  },
  components: {
    Title: {
      styles: {
        root: {
          color: 'light-dark(var(--mantine-color-black), var(--mantine-color-white))',
        },
      },
    },
    Text: {
      styles: {
        root: {
          color: 'light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))',
        },
      },
    },
    Button: {
      defaultProps: {
        fw: 600,
      },
    },
    TextInput: {
      styles: {
        label: { fontWeight: 600, marginBottom: 4 },
      },
    },
    PasswordInput: {
      styles: {
        label: { fontWeight: 600, marginBottom: 4 },
      },
    },
    Paper: {
      defaultProps: {
        withBorder: true,
        radius: 'md',
      },
      styles: {
        root: {
          backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-6))',
        },
      },
    },
    AppShell: {
      styles: {
        main: {
          backgroundColor: 'light-dark(#f8f9fa, var(--mantine-color-dark-7))',
        },
        header: {
          backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-7))',
          borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-5))',
        },
        navbar: {
          backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-7))',
          borderRight: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-5))',
        },
      },
    },
    Modal: {
      styles: {
        content: {
          backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-6))',
        },
        header: {
          backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-6))',
          borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-5))',
        },
      },
    },
  },
});

function App() {
  return (
    <QueryClientProvider client={queryClient}>
      <MantineProvider theme={theme} defaultColorScheme="light">
        <RouterProvider router={router} />
      </MantineProvider>
    </QueryClientProvider>
  );
}

export default App;
