FROM node:22-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM ghcr.io/baogutang/api-monitor:latest
USER root
COPY --from=web-build /src/web/dist /app/web/dist
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
USER app

ENTRYPOINT ["/entrypoint.sh"]
