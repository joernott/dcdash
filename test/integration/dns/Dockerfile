# bind9 primary with an SFTP-only user for the DNS integration tests.
# NOT for production use: all keys are test-only.
FROM internetsystemsconsortium/bind9:9.20
RUN apk add --no-cache openssh-server
COPY primary-entrypoint.sh /primary-entrypoint.sh
ENTRYPOINT ["/bin/sh", "/primary-entrypoint.sh"]
