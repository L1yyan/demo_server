PROTO_DIR := pb
GEN_DIR := gen
PROTO_FILES := $(shell find $(PROTO_DIR) -name '*.proto')

.PHONY: proto clean-proto

# proto 生成 protobuf 与 gRPC Go 代码
proto:
	@if [ -z "$(PROTO_FILES)" ]; then \
		echo "[proto] 未找到 proto 文件，目录: $(PROTO_DIR)"; \
		exit 1; \
	fi
	@echo "[proto] 开始生成 protobuf 与 gRPC Go 代码"
	@echo "[proto] proto 目录: $(PROTO_DIR)"
	@echo "[proto] 生成目录: $(GEN_DIR)"
	@echo "[proto] proto 文件: $(PROTO_FILES)"
	@protoc \
		--proto_path=. \
		--go_out=. \
		--go_opt=module=demo_server \
		--go-grpc_out=. \
		--go-grpc_opt=module=demo_server \
		$(PROTO_FILES)
	@echo "[proto] 生成成功，输出目录: $(GEN_DIR)"

# clean-proto 清理生成的 protobuf 代码
clean-proto:
	@echo "[proto] 清理生成目录: $(GEN_DIR)"
	@rm -rf $(GEN_DIR)
	@echo "[proto] 清理完成"
