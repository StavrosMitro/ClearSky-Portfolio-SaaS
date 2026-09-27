// Package contracts holds what every ClearSky service shares: the RPC
// envelope, RabbitMQ topology, synchronisation messages, deterministic IDs,
// observability, health checks and Postgres helpers. Services import the
// sub-packages; this root package only documents the module.
package contracts
