SITES := site1 site2

.PHONY: up down logs status kafka-status kafka-topics cut-% restore-%

up: config/generated/site1.conf              ## start Cloud Kafka, cloud-side bridges, and site NATS servers
	docker compose up -d
	@until docker compose logs setup 2>/dev/null | grep -q 'setup complete'; do sleep 1; done
	@docker compose logs setup
	@until docker compose logs kafka-setup 2>/dev/null | grep -q 'kafka setup complete'; do sleep 1; done
	@docker compose logs kafka-setup
	@until curl -sf http://localhost:8225/healthz >/dev/null 2>&1; do sleep 1; done
	@until curl -sf http://localhost:8226/healthz >/dev/null 2>&1; do sleep 1; done
	@echo "cloud bridges healthy: site1-bridge (:8225), site2-bridge (:8226)"

down:                                        ## stop and delete all data
	docker compose down -v

logs:
	docker compose logs -f kafka site1-bridge site2-bridge site1 site2

status:                                      ## site streams and consumers
	@for s in $(SITES); do \
	  echo "=== $$s ==="; \
	  docker compose run --rm --entrypoint sh setup -c \
	    "nats --server nats://$$s:4222 --user site-ops --password \$$SITE_OPS_PASS stream report"; \
	done

kafka-status:                                ## per-site Kafka consumer group lag
	@for s in $(SITES); do \
	  docker compose exec kafka /opt/kafka/bin/kafka-consumer-groups.sh \
	    --bootstrap-server localhost:9092 --describe --group kafka-bridge-$$s; \
	done

kafka-topics:                                ## list topics and partitions
	docker compose exec kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --describe

cut-%:                                       ## make cut-site1: sever VPN/WAN between site1 and cloud bridge
	docker network disconnect nats-c-wan c-$*

restore-%:                                   ## make restore-site1: restore VPN/WAN link
	docker network connect nats-c-wan c-$*

config/generated/site1.conf: scripts/generate.sh Makefile
	scripts/generate.sh $(SITES)
