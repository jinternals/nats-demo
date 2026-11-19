SITES     := site1 site2
HUB_NAMES := cloud-hub,cloud-hub-1,cloud-hub-2,cloud-hub-3,localhost

.PHONY: up down test logs status cut-% restore-% kill-% start-%

up: certs/ca.crt config/hub-accounts.conf   ## start everything and create streams
	docker compose up -d
	docker compose logs -f setup | sed -n '/setup complete/q;p'

down:                                        ## stop and delete all data
	docker compose down -v

test:                                        ## end-to-end tests against the running stack
	cd e2e && go test -v -count=1 -timeout 20m ./...

logs:
	docker compose logs -f cloud-hub-1 cloud-hub-2 cloud-hub-3 $(SITES)

status:                                      ## streams, leaders and replicas
	docker compose run --rm --entrypoint sh setup -c \
	  'nats --server tls://cloud-hub-1:4222 --tlsca /certs/ca.crt --user cloud-app --password $$CLOUD_APP_PASS stream report'

cut-%:                                       ## make cut-site1: take the site's WAN link down
	docker network disconnect nats-edge-wan $*

restore-%:                                   ## make restore-site1: bring it back
	docker network connect nats-edge-wan $*

kill-%:                                      ## make kill-cloud-hub-2: hard-kill a server
	docker kill -s KILL $*

start-%:
	docker start $*

certs/ca.crt:
	scripts/gen-certs.sh certs $(HUB_NAMES) $(SITES)

config/hub-accounts.conf: scripts/gen-hub-accounts.sh Makefile
	scripts/gen-hub-accounts.sh $(SITES) > $@
