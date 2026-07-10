FROM postgres:15-alpine

# Copy the schema initialization script
COPY srcs/schema.sql /docker-entrypoint-initdb.d/

# Non-secret defaults only. POSTGRES_PASSWORD is intentionally NOT defaulted
# here so a missing DB_PASSWORD fails the build/run instead of silently booting
# with a well-known credential. It is supplied at runtime via the .env file.
ENV POSTGRES_DB=events_db
ENV POSTGRES_USER=events_user
