# Woodpecker 前端镜像：Node 构建静态产物 → nginx 托管并反代 /api。
# 构建上下文 = web/：docker build -t woodpecker-web ./web
FROM node:22-alpine AS builder

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

# nginx 托管 SPA 静态文件 + /api 反代（SSE 直通配置见 nginx.conf）
FROM nginx:1.27-alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80
HEALTHCHECK --interval=15s --timeout=3s --retries=3 \
  CMD wget -qO- http://127.0.0.1/ >/dev/null 2>&1 || exit 1
