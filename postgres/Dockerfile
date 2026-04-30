FROM postgres:15-alpine

# Copy the schema initialization script
COPY srcs/schema.sql /docker-entrypoint-initdb.d/

# Set default environment variables
ENV POSTGRES_DB=voting_db
ENV POSTGRES_USER=voting_user
ENV POSTGRES_PASSWORD=voting_password
