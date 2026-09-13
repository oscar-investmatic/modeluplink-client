FROM --platform=$BUILDPLATFORM golang:1.27.1-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends gcc-mingw-w64-x86-64 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
COPY proto ./proto
COPY windows ./windows
COPY desktop ./desktop
COPY LICENSE NOTICE ./
ARG MODELUPLINK_REVISION
ENV MODELUPLINK_REVISION=$MODELUPLINK_REVISION
ARG MODELUPLINK_VERSION
RUN mkdir -p /out && cp LICENSE NOTICE /out/
RUN test -n "$MODELUPLINK_REVISION"
RUN go run ./windows/resources -version "${MODELUPLINK_VERSION:-$(cat desktop/VERSION)}" -windres x86_64-w64-mingw32-windres
ENV GOOS=windows GOARCH=amd64 CGO_ENABLED=1 CC=x86_64-w64-mingw32-gcc
RUN --mount=type=cache,target=/root/.cache/go-build go build -mod=readonly -trimpath -ldflags="-s -w -X github.com/oscar-investmatic/modeluplink-client/internal/buildinfo.Revision=$MODELUPLINK_REVISION -H=windowsgui -X github.com/oscar-investmatic/modeluplink-client/pkg/agent.Version=${MODELUPLINK_VERSION:-$(cat desktop/VERSION)}" -o /out/modeluplink.exe ./cmd/modeluplink
RUN --mount=type=cache,target=/root/.cache/go-build go build -mod=readonly -trimpath -ldflags="-s -w -X github.com/oscar-investmatic/modeluplink-client/internal/buildinfo.Revision=$MODELUPLINK_REVISION -H=windowsgui" -o /out/modeluplink-app.exe ./cmd/modeluplink-app
FROM scratch AS artifacts
COPY --from=build /out/ /
