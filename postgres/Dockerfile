FROM postgres:15-alpine

# No /docker-entrypoint-initdb.d/ script: the schema lives in
# backend/srcs/migrations/ and the backend applies it with goose at startup.
# initdb only ever ran on an empty data directory, so it could not deliver
# schema changes to a database that already had data (issue #33).

# Non-secret defaults only. POSTGRES_PASSWORD is intentionally NOT defaulted
# here so a missing DB_PASSWORD fails the build/run instead of silently booting
# with a well-known credential. It is supplied at runtime via the .env file.
ENV POSTGRES_DB=events_db
ENV POSTGRES_USER=events_user
