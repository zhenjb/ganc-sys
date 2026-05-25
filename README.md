# GANC System Backend

Backend API for the GANC/ZKDEX project.

## Requirements

- Go 1.22+
- Git

## Clone

```bash
git clone https://github.com/zhenjb/ganc-sys.git
cd ganc-sys
````

## Run

```bash
go run ./cmd/api
```

The backend will start at:

```txt
http://localhost:8080
```

## Health check

```bash
curl -s http://localhost:8080/api/health
```

Expected response:

```json
{
  "status": "ok",
  "service": "offchain-backend"
}
```

## API testing

Use the provided Postman collection for all API flows.

Postman collection: `docs/postman/ganc_sys_int01_int04.postman_collection.json`
````
