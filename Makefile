BINARY = simple-cni-plugin
GITHUB_USER = mayooot
IMG=registry.cn-hangzhou.aliyuncs.com/mayooot/simple-cni-plugin
VERSION=v0.3
GOARCH = amd64
GOBUILD=CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) go build

build: clean
	$(GOBUILD) -o bin/simple-cni-plugin cmd/simple-cni-plugin/main.go
	$(GOBUILD) -o bin/simple-cni-plugin-daemonset cmd/simple-cni-plugin-daemonset/main.go

imports:
	goimports-reviser --rm-unused -local github.com/${GITHUB_USER}/${BINARY} -format ./...

docker-build:
	docker build --rm --platform linux/amd64 -t $(IMG):$(VERSION) .
docker-push:
	docker push $(IMG):$(VERSION)

deploy:
	kubectl apply -f deploy/mycni.yaml

clean:
	rm -rf bin
	go mod tidy

kind-cluster:
	kind create cluster --config deploy/kind.yaml

kind-image-load:
	kind load docker-image $(IMG):$(VERSION)

deploy-alpine:
	kubectl create deployment cni-test --image=alpine --replicas=6 -- top



.PHONY: build deploy clean imports