# -------------------------------------------------------------
# Stage 1: build the React web console
# -------------------------------------------------------------
FROM node:22-alpine AS web-build
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# -------------------------------------------------------------
# Stage 2: compile the custom Go backend
# -------------------------------------------------------------
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
RUN go mod download

COPY . .
COPY --from=web-build /src/web/dist ./web/dist

ARG VERSION=1.2.3
ARG COMMIT=unknown
ARG DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X api-monitor/internal/version.Version=${VERSION} -X api-monitor/internal/version.Commit=${COMMIT} -X api-monitor/internal/version.Date=${DATE}" \
    -o /out/api-monitor ./cmd/api-monitor

# -------------------------------------------------------------
# Stage 3: minimal runtime image
# -------------------------------------------------------------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S app && adduser -S app -G app
WORKDIR /app

COPY --from=build /out/api-monitor /usr/local/bin/api-monitor
COPY migrations ./migrations
COPY --from=web-build /src/web/dist ./web/dist
COPY entrypoint.sh /entrypoint.sh
RUN sed -i 's/\r$//' /entrypoint.sh \
    && chmod +x /entrypoint.sh /usr/local/bin/api-monitor

USER app
EXPOSE 8080
ENTRYPOINT ["/entrypoint.sh"]
