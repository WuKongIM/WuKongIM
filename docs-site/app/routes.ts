import { route, type RouteConfig } from '@react-router/dev/routes';

export default [
  route('*', 'routes/docs.tsx'),
] satisfies RouteConfig;
