protos:
	cd internal/protocol; make protos

test_concurrent_connect: COUNT := 50
test_concurrent_connect:
	@mkdir -p bin
	@rm -f bin/test_concurrent_connect.log
	@touch bin/test_concurrent_connect.log; \
	for i in {1..$(COUNT)}; do \
		go test -v -run=TestServiceConcurrentConnect >> bin/test_concurrent_connect.log 2>&1; \
		sleep 5; \
	done

.PHONY: protos test_concurrent_connect