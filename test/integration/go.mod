module github.com/Lamy210/go-template/test/integration

go 1.27.0

toolchain go1.27.1

require (
	github.com/Lamy210/go-template v0.0.0
	github.com/jackc/pgx/v5 v5.11.0
	github.com/testcontainers/testcontainers-go v0.44.0
	github.com/testcontainers/testcontainers-go/modules/postgres v0.44.0
)

replace github.com/Lamy210/go-template => ../..
