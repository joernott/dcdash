# SPDX-License-Identifier: GPL-3.0-or-later
# Container image of dcdash, built by GoReleaser (.goreleaser.yaml) from the release
# binaries. distroless/static: no shell, no package manager, runs as nonroot (65532).
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/dcdash /usr/bin/dcdash
# The data directory must belong to nonroot; distroless has no shell to create it.
COPY --chown=nonroot:nonroot --chmod=0700 packaging/container-data/ /var/lib/dcdash/
# data_dir; mount a volume here. The configuration goes to /etc/dcdash/config.yaml.
VOLUME ["/var/lib/dcdash"]
EXPOSE 8443
USER nonroot:nonroot
ENTRYPOINT ["/usr/bin/dcdash"]
CMD ["--config", "/etc/dcdash/config.yaml", "serve"]
