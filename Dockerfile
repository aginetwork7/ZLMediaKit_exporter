FROM arm64v8/golang:1.25.9-bookworm AS builder

ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG BRANCH=unknown
ARG BUILD_DATE=unknown
# Behind a restricted network, pass e.g. --build-arg GOPROXY=https://goproxy.cn,direct
ARG GOPROXY=https://proxy.golang.org,direct

RUN --mount=type=cache,target=/gocache \
    --mount=type=bind,target=.,source=.,readonly=false \
    GOMODCACHE=/gocache \
    GOPROXY=${GOPROXY} \
    CGO_ENABLED=0 \
    go build \
          -trimpath \
          -ldflags "\
            -s -w \
            -X github.com/prometheus/common/version.Version=${VERSION} \
            -X github.com/prometheus/common/version.Revision=${COMMIT_SHA} \
            -X github.com/prometheus/common/version.Branch=${BRANCH} \
            -X github.com/prometheus/common/version.BuildDate=${BUILD_DATE} \
            -X main.BuildVersion=${VERSION} \
            -X main.BuildCommitSha=${COMMIT_SHA} \
            -X main.BuildDate=${BUILD_DATE}" \
          -o /bin/zlm_exporter \
          .

RUN /bin/zlm_exporter --version

FROM arm64v8/debian:bookworm-20240311-slim

ARG GROUP_ID=1000
ARG USER_ID=1000
ARG USER=agi7
ARG GROUP=agi7

ENV DEBIAN_FRONTEND=noninteractive
WORKDIR /app

# Create non-root user
RUN groupadd -g ${GROUP_ID} ${GROUP} \
 && useradd -u ${USER_ID} -g ${GROUP} -m -s /bin/sh ${USER}

# Install only required packages
RUN apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y --no-install-recommends \
      ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --chown=${USER}:${GROUP} --from=builder /bin/zlm_exporter .

# Switch to non-root user
USER ${USER}

EXPOSE 9101

ENTRYPOINT ["./zlm_exporter"]
