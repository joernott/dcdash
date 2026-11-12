# Security

This document covers how dcdash protects its own data. Authentication, sessions and roles are
added in Sprints 3 and 4 and documented here as they are implemented.

## Data at rest

dcdash keeps its state in an SQLite database, `<data_dir>/dcdash.db`. The data directory is
created with mode `0700` and the database file with mode `0600`.

Secrets that dcdash has to read back later, such as TOTP seeds, are additionally encrypted in
the database with **AES-256-GCM**:

- Each encrypted value carries a random nonce and the ID of the key that encrypted it.
- Each value is bound to its purpose with *associated data* (for example `totp:<user>`), so an
  encrypted value can't be moved to another field or user.
- Secrets that only need to be compared are **hashed instead**: recovery codes and session
  tokens are never stored in plain text or in a reversible form.

### The encryption key

```yaml
security:
  encryption_key: "file:/etc/dcdash/encryption.key"
```

Generate a key with `dcdash gen-key` (32 random bytes, base64-encoded) and store it outside the
configuration file, readable only by the dcdash user:

```sh
umask 077
dcdash gen-key > /etc/dcdash/encryption.key
chown dcdash: /etc/dcdash/encryption.key
```

**Back up the key separately from the database.** Without it, the encrypted secrets can't be
recovered. For TOTP this means every user has to enroll again after an administrator reset.

At the first start, dcdash stores a check value encrypted with the key. If dcdash is later
started with a different key, it refuses to start instead of failing on individual records:

```text
security.encryption_key does not match the database; if the key was changed,
list the old key in security.previous_encryption_keys
```

### Key rotation

Rotate the key when it may have been exposed, or according to your policy:

1. Generate a new key and make it the `encryption_key`. Move the old key to
   `previous_encryption_keys`:

   ```yaml
   security:
     encryption_key: "file:/etc/dcdash/encryption-2027.key"
     previous_encryption_keys:
       - "file:/etc/dcdash/encryption.key"
   ```

2. Restart dcdash. Data encrypted with the old key stays readable. Everything written from now
   on uses the new key, and the key check value is re-encrypted right away.
3. Re-encrypt the remaining data with the new key. Until a re-encryption command is available
   (it comes with the first encrypted data, TOTP in Sprint 4), records are re-encrypted when
   they are next written.
4. Once all data is re-encrypted, remove the old key from `previous_encryption_keys` and
   destroy it.

Several previous keys can be listed. Keys are identified by a fingerprint stored with each
value, so their order doesn't matter.
