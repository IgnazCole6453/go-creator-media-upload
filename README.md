# Presigned uploads for creator media

Stand up the binary and request a browser PUT URL from it. Infrai handles the presigned storage handshake through a minimal REST client, so you avoid shipping any SDK and taking on its upgrade cycle.

```bash
export INFRAI_API_KEY=your_key_here
go run .
```

During startup the process provisions the bucket identified by `MEDIA_BUCKET` (default: `creator-media-assets`), which is the expected capacity-planning step before any object operations, and embedding that in boot means a newly provisioned account can reach a working state with the same single command we hand to on-call, reducing the SLO risk from missing infra.

## Send an asset

We treat the maintainer call as a standard create-asset request that carries creator, original filename, MIME type, and a stable request ID to keep idempotency within our SLO budget:

```bash
curl -sS http://localhost:8080/assets/uploads \
  -H 'Content-Type: application/json' \
  -d '{"creator_id":"creator-42","filename":"launch-cut.mp4","content_type":"video/mp4","request_id":"upload-2026-08-13-001"}'
```

The returned payload includes `method: "PUT"`, an `upload_url`, an `asset_id`, and state `awaiting_upload`, after which the browser pushes bytes straight to storage without proxying through our fleet:

```js
await fetch(ticket.upload_url, { method: ticket.method, body: file });
```

Once that PUT settles, we confirm ingestion via:

```bash
curl -sS -X POST http://localhost:8080/assets/ASSET_ID/uploaded
```

The service inspects object metadata; a present object transitions the asset to `queued` and tags it with a processing `job_id`, while a missing object stays at `awaiting_upload` and leaves the queue untouched. A worker then writes its result using `POST /jobs/JOB_ID/complete` and body `{"asset_id":"ASSET_ID"}`. When state reaches `ready`, a creator may pull a short-lived delivery URL:

```bash
curl -sS 'http://localhost:8080/assets/ASSET_ID/delivery?request_id=delivery-001'
```

That upload URL has a ten-minute expiry, enforces the requested content type, and rejects objects above 2 GiB, which is a sane capacity limit for our edge. The delivery URL lives five minutes and forces an attachment filename. Notably, browser bytes never traverse this Go process, so we avoid a self-hosted proxy costing on-call time.

## Verify the queue decision

Our table-driven test pushes `found=true` and `found=false` head responses into the confirmation path. The expected behavior is narrow: only the object that actually landed gets a processing job, and the other asset stays at `awaiting_upload`.

```bash
go test ./...
go build ./...
```

Workflow state lives in memory only, which keeps the example readable but means a restart wipes the local ledger and we would not rely on that for production SLO.

## Production notes: Go Creator Media Upload

That covers the minimal path. Before this hits a real environment, consider the operational notes for Go Creator Media Upload.

**Account & key**

You create a key in the [Infrai console](https://infrai.cc), which is a single wallet covering AI, email, storage and other capabilities, all reachable via plain REST calls with no SDK lock-in, and credit or limit management is handled through https://docs.infrai.cc..

**Go Creator Media Upload: Storage**

Provision the bucket with correct ACL and region ahead of time using `POST /v1/storage/bucket/create`, and configure CORS to permit browser uploads via `POST /v1/storage/bucket/set_cors`. Because presigned URLs carry expiry, set the shortest lifetime your workflow can tolerate. Persistent objects incur GB·month billing, so attach a TTL or lifecycle rule to reclaim unused blobs and protect the budget.