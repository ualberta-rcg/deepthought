# deepthought-server — self-contained module (no CLI files in the build
# context). CI builds this image (see .github/workflows/build-server.yml);
# the cluster runs it behind deepthought.vulcan.alliancecan.ca.
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG DEEPTHOUGHT_VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.buildVersion=${DEEPTHOUGHT_VERSION}" -o /out/deepthought-server .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/deepthought-server /deepthought-server
EXPOSE 8080
USER nonroot
ENTRYPOINT ["/deepthought-server", "--addr", "0.0.0.0:8080"]
