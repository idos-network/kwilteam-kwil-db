# CI image for acceptance tests. kwild and kwil-cli are built on the runner
# (see .github/workflows/pr.yaml) and the build context is the .build dir.
# Release images use kwild.dockerfile, which compiles inside Docker.
FROM ubuntu:24.04
WORKDIR /app
RUN mkdir -p /var/run/kwil && chmod 777 /var/run/kwil
RUN apt update && apt install -y postgresql-client curl ca-certificates
COPY kwild kwil-cli ./
EXPOSE 8484 6600
ENTRYPOINT ["/app/kwild"]
