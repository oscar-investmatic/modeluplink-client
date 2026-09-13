FROM golang:1.27.1-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends \
    libgl1-mesa-dev libx11-dev libxcursor-dev libxrandr-dev libxinerama-dev \
    libxi-dev libxxf86vm-dev libwayland-dev libxkbcommon-dev xvfb xauth \
    dbus-x11 desktop-file-utils imagemagick appstream && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
COPY proto ./proto
COPY linux ./linux
COPY desktop ./desktop
ARG MODELUPLINK_REVISION
ENV MODELUPLINK_REVISION=$MODELUPLINK_REVISION
ARG MODELUPLINK_VERSION
ENV MODELUPLINK_VERSION=$MODELUPLINK_VERSION
RUN go test -race -tags ci ./cmd/modeluplink-app ./internal/service
RUN bash linux/package.sh
RUN dpkg-deb -x dist/linux/*.deb /tmp/uplink-package && \
    desktop-file-validate /tmp/uplink-package/usr/share/applications/com.modeluplink.app.desktop && \
    appstreamcli validate --no-net /tmp/uplink-package/usr/share/metainfo/com.modeluplink.app.metainfo.xml && \
    ldd /tmp/uplink-package/usr/lib/modeluplink/modeluplink-app && \
    ! ldd /tmp/uplink-package/usr/lib/modeluplink/modeluplink-app | grep 'not found'
RUN bash linux/smoke.sh /tmp/uplink-package
FROM scratch AS artifacts
COPY --from=build /src/dist/linux/ /
