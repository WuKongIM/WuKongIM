build:
	docker build -t wukongim .
push:
	docker tag wukongim wukongim/wukongim:latest-dev
	docker push wukongim/wukongim:latest-dev
deploy:
	docker build -t wukongim .
	docker tag wukongim wukongim/wukongim:latest
	docker push wukongim/wukongim:latest	
deploy-dev:
	docker build -t wukongim .
	docker tag wukongim wukongim/wukongim:latest-dev
	docker push wukongim/wukongim:latest-dev
deploy-arm:
	docker build -t wukongimarm64 . -f Dockerfile.arm64 --platform linux/arm64
	docker tag wukongimarm64 wukongim/wukongim:latest-arm64
	docker push wukongim/wukongim:latest-arm64
deploy-v2-dev:
	docker build -t wukongim . 
	docker tag wukongim registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.0.2-beta-20240805-dev
	docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.0.2-beta-20240805-dev
deploy-v2:
	docker build -t wukongim .
	docker tag wukongim registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.0.2-beta-20240805
	docker tag wukongim wukongim/wukongim:v2.0.2-beta-20240805
	docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v2.0.2-beta-20240805
	docker push wukongim/wukongim:v2.0.2-beta-20240805
deploy-latest:
	docker build -t wukongim .
	docker tag wukongim registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v1.2
	docker tag wukongim wukongim/wukongim:v1.2
	docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v1.2
	docker push wukongim/wukongim:v1.2
deploy-v1.2.x:
	docker build -t wukongim .
	docker tag wukongim registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v1.2.5
	docker tag wukongim wukongim/wukongim:v1.2.5
	docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v1.2.5
	docker push wukongim/wukongim:v1.2.5

# docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v1.2
# docker push registry.cn-shanghai.aliyuncs.com/wukongim/wukongim:v1.2-dev
