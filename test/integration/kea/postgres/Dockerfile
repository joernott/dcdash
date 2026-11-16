# PostgreSQL with the Kea 3.0 database schema, for the integration tests.
# Neither the Kea images nor ISC's packages contain kea-admin or the schema, so
# it comes from the Kea source archive, pinned by its SHA-256 (the archive's
# ISC signature was checked when the hash was taken, see docs/adr/0003-kea-api.md).
FROM postgres:17-alpine AS schema
RUN apk add --no-cache xz
ADD --checksum=sha256:62d32494a7ac62d130149ae2f1c6ea5c3c28ff4caa15cc6e1b9812ffb4158282 \
    https://downloads.isc.org/isc/kea/3.0.4/kea-3.0.4.tar.xz /tmp/kea.tar.xz
RUN tar -xJf /tmp/kea.tar.xz -O kea-3.0.4/src/share/database/scripts/pgsql/dhcpdb_create.pgsql \
    > /tmp/dhcpdb_create.pgsql

FROM postgres:17-alpine
COPY --from=schema /tmp/dhcpdb_create.pgsql /docker-entrypoint-initdb.d/10-kea.sql
