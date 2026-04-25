FROM oven/bun:1.3.11-alpine AS builder
WORKDIR /app

COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile

COPY web/ ./
RUN bun run build

FROM nginx:1.27-alpine
ENV WK_WEB_API_URL=http://wk-node1:5301

COPY web/nginx.conf.template /etc/nginx/templates/default.conf.template
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80
