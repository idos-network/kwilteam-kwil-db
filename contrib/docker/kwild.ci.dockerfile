# CI image for acceptance tests. kwild and kwil-cli are built on the runner
# (see .github/workflows/pr.yaml) and the build context is the .build dir.
# Release images use kwild.dockerfile, which compiles inside Docker.
#
# Based on the postgres image rather than ubuntu: it ships the pg_dump/psql
# 16.15 that kwild requires, the job already pulls it for kwil-postgres, and
# GitHub runners (image 20261028) can't resolve archive.ubuntu.com.
FROM postgres:16.15-bookworm
WORKDIR /app
RUN mkdir -p /var/run/kwil && chmod 777 /var/run/kwil
RUN apt-get update \
	&& apt-get install -y --no-install-recommends curl ca-certificates \
	&& rm -rf /var/lib/apt/lists/*
COPY kwild kwil-cli ./
EXPOSE 8484 6600
ENTRYPOINT ["/app/kwild"]
