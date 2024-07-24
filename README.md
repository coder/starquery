<h1>
    <img src="./icon.png" width="48px" style="margin-right: 12px;" align="center">
    starquery
</h1>

[!["Join us on
Discord"](https://badgen.net/discord/online-members/coder)](https://coder.com/chat?utm_source=github.com/coder/vscode-coder&utm_medium=github&utm_campaign=readme.md)

Query in near-realtime if a user has starred a GitHub repository.

```
https://starquery.coder.com/coder/coder/user/kylecarbs
```

- Uses GitHub Webhooks for near-realtime accuracy.
- Periodically refreshes all stargazers using GitHub's GraphQL API for accuracy.
- Start tracking a repository by [adding it to the list](https://github.com/coder/starquery/blob/main/cmd/starquery/main.go#L52)!

This service is used by [coder/coder](https://github.com/coder/coder) to prompt users to star the repository if they haven't already!

## Deployment

starquery is hosted at [starquery.coder.com](https://starquery.coder.com). Not all repositories are tracked by default (that'd be a lot to handle!). Feel free to repositories [here](https://github.com/coder/starquery/blob/main/cmd/starquery/main.go#L52).

To run starquery, `GITHUB_TOKEN` and `REDIS_URL` are required. `WEBHOOK_SECRET` must be set if accepting Webhooks from GitHub's API.

### Hosted

The `./deploy.sh` script can be used to update the service (probably should be automated at some point).

`/run/starquery/environ` must exist. Here is a template:

```env
REDIS_URL=127.0.0.1:6379
BIND_ADDRESS=127.0.0.1:8080
# use cdrci account
GITHUB_TOKEN=
WEBHOOK_SECRET=
```

To set up the Cloudflare Tunnel, see [the config file](./cloudflared.yaml). `cloudflared` is ran in `screen -S cloudflared`:

```sh
cloudflared tunnel run 7e5e3b0d-4eb3-4aff-9924-e5f6efebcc2d
```

To set up a GitHub webhook:

1. Head to the "New Webhook" page (e.g. https://github.com/coder/starquery/settings/hooks/new).
2. Set the payload URL to `https://starquery.coder.com/webhook`.
3. Click "Let me select individual events.", uncheck "Push", check "Stars".

Delivery should succeed for the initial ping!
