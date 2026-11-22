#!/bin/sh
# Creates the Kafka topics used by nats-io/nats-kafka. Runs once on `docker compose up`.
# Safe to re-run: --if-not-exists leaves existing topics untouched.
set -eu
: "${SITES:?set SITES, e.g. \"site1 site2\"}"
BROKERS="${KAFKA_BROKERS:-kafka:9092}"
COMMAND_PARTITIONS="${COMMAND_PARTITIONS:-1}"   # 1 partition per site keeps commands strictly ordered
UP_PARTITIONS="${UP_PARTITIONS:-6}"             # keyed by <site>, so each site stays on one partition
REPLICATION="${TOPIC_REPLICATION:-1}"
UP_TYPES="${UP_TYPES:-telemetry health result}"

create_topic() {
  /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server "$BROKERS" \
    --create --if-not-exists \
    --topic "$1" \
    --partitions "$2" \
    --replication-factor "$REPLICATION"
  echo "ensured topic $1 ($2 partition(s))"
}

for s in $SITES; do
  create_topic "cloud.to.site.$s.command" "$COMMAND_PARTITIONS"
done
for typ in $UP_TYPES; do
  create_topic "site.to.cloud.$typ" "$UP_PARTITIONS"
done

echo "kafka setup complete"
