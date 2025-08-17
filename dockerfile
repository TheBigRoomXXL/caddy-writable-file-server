# ╔═════════════════════════════════════════════════╗
# ║                  BUILD STAGE                    ║
# ╚═════════════════════════════════════════════════╝
FROM golang:1.24-alpine3.22 AS build

WORKDIR /build

# Install system dependencies
RUN apk add upx xcaddy
COPY go.mod go.sum ./
RUN go mod download

# Build static binary
COPY . . 
RUN CGO_ENABLED=0 GOFLAGS="-ldflags=-s -w -tags=timetzdata" xcaddy build --with github.com/thebigroomxxl/caddy-site-deployer

# Compress binary
RUN upx /build/caddy

# Create the user file 
RUN adduser -H -D www

# Get default caddyfile
RUN wget -O /build/Caddyfile "https://github.com/caddyserver/dist/raw/33ae08ff08d168572df2956ed14fbc4949880d94/config/Caddyfile"

# ╔═════════════════════════════════════════════════╗
# ║               PRODUCTION STAGE                  ║
# ╚═════════════════════════════════════════════════╝
FROM scratch AS production

# Create a user to avoid runing as root
COPY --from=build /etc/passwd /etc/passwd
USER www
WORKDIR /app

# Copy the static build
COPY --chown=www:www --from=build /build/caddy /bin/caddy

# Copy default config
COPY --chown=www:www --from=build /build/Caddyfile  /etc/caddy/Caddyfile

# Copy the certificates authorities
COPY --from=alpine:latest /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

CMD ["/bin/caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
