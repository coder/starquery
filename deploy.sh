#!/usr/bin/env bash

set -euxo pipefail
cd "$(dirname "$0")"

goreleaser release --snapshot --clean
gcloud compute scp --project "coder-starquery" ./dist/*.deb kyle@starquery:~/starquery.deb
gcloud compute ssh --project "coder-starquery" kyle@starquery -- "sudo dpkg -i ~/starquery.deb && sudo systemctl daemon-reload && sudo service starquery restart"
