# Debian 13 + 1Panel 部署

此模板只启动 Aihub，复用 1Panel 已安装的 PostgreSQL 和 Redis。应用数据固定保存到 `/data/aihub/data`。

## 准备

确认 PostgreSQL、Redis 和新应用能够加入同一个 Docker 网络：

```bash
docker network ls | grep 1panel
docker ps --format 'table {{.Names}}\t{{.Networks}}'
```

默认网络名为 `1panel-network`。把 PostgreSQL、Redis 的容器名分别填入 `.env` 的 `DATABASE_HOST` 和 `REDIS_HOST`。

## 安装

```bash
mkdir -p /data/aihub/data
cd /data/aihub
cp .env.example .env
chmod 600 .env
openssl rand -hex 32
openssl rand -hex 32
```

将两次生成的值分别写入 `JWT_SECRET` 和 `TOTP_ENCRYPTION_KEY`，同时填写数据库、Redis 和管理员配置。

镜像和 Release 均公开，无需登录 GitHub：

```bash
docker compose pull
docker compose up -d
docker compose logs -f aihub
```

在 1Panel 网站中添加反向代理，目标填写 `http://127.0.0.1:8080`。

## 更新

面板中的橙色提示会下载自定义 GitHub Release 并替换容器内二进制，随后点击重启即可生效。蓝色提示只表示 Sub2API 上游有新版本。

重建容器时建议同步修改 `.env` 中的精确镜像标签：

```bash
cd /data/aihub
docker compose pull
docker compose up -d
```
