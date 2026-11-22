SITES     := site1 site2
HUB_NAMES := cloud-hub,cloud-hub-1,cloud-hub-2,cloud-hub-3,localhost

.PHONY: up down test logs status kafka-status kafka-topics cut-% restore-% kill-% start-%

up: certs/ca.crt config/generated/hub-accounts.conf   ## start everything and create streams and topics
	docker compose up -d
	@until docker compose logs setup 2>/dev/null | grep -q 'setup complete'; do sleep 1; done
	@docker compose logs setup
	@until docker compose logs kafka-setup 2>/dev/null | grep -q 'kafka setup complete'; do sleep 1; done
	@docker compose logs kafka-setup
	@until curl -sf http://localhost:8225/healthz >/dev/null 2>&1; do sleep 1; done
	@echo "kafka-bridge healthy (http://localhost:8225/varz)"

down:                                        ## stop and delete all data
	docker compose down -v

test:                                        ## end-to-end tests against the running stack
	cd e2e && go test -v -count=1 -timeout 30m ./...

logs:
	docker compose logs -f kafka kafka-bridge cloud-hub-1 cloud-hub-2 cloud-hub-3 $(SITES)

status:                                      ## streams, leaders and replicas
	docker compose run --rm --entrypoint sh setup -c \
	  'nats --server tls://cloud-hub-1:4222 --tlsca /certs/ca.crt --user cloud-app --password $$CLOUD_APP_PASS stream report'

kafka-status:                                ## the bridge's lag on both sides
	docker compose exec kafka /opt/kafka/bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 --describe --group kafka-bridge
	docker compose run --rm --entrypoint sh setup -c \
	  'nats --server tls://cloud-hub-1:4222 --tlsca /certs/ca.crt --user cloud-app --password $$CLOUD_APP_PASS consumer report cloud-inbox'

kafka-topics:                                ## list topics and partitions
	docker compose exec kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --describe

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

config/generated/hub-accounts.conf: scripts/generate.sh Makefile
	scripts/generate.sh $(SITES)
