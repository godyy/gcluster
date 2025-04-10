PROTO_PATH = ./protos
PB_PATH = ./pb
PB_MODULE_PREFIX = github.com/godyy/gcluster/net/internal/protocol/pb


.PHONY: protos
protos:
	rm -rf pb && mkdir pb
	@for file in $(shell find ./protos -type f -name "*.proto"); do \
		echo $$file; \
		protoc -I=$(PROTO_PATH) --go_out=$(PB_PATH) --go_opt=module=$(PB_MODULE_PREFIX) $$file; \
	done
	