# starquery

An API that near-realtime tracks whether a user has starred a GitHub repository.

## Deployment

The API is live at `starquery.coder.com`. A Cloudflare Tunnel is used for DDoS protection made with [this guide](https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/get-started/create-local-tunnel/). The config is:

```yaml
# Located in /home/kyle/.cloudflared/config.yml
url: http://localhost:8080
tunnel: 7e5e3b0d-4eb3-4aff-9924-e5f6efebcc2d
credentials-file: /home/kyle/.cloudflared/7e5e3b0d-4eb3-4aff-9924-e5f6efebcc2d.json
```

`cloudflared` is ran in `screen -S cloudflared`:

```
cloudflared tunnel run 7e5e3b0d-4eb3-4aff-9924-e5f6efebcc2d
```

`/run/starquery/environ` must have `WEBHOOK_SECRET`, but here's a template:

```env
REDIS_URL=127.0.0.1:6379
BIND_ADDRESS=127.0.0.1:8080
# use cdrci account
GITHUB_TOKEN=
```
