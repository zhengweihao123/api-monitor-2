#!/bin/sh
set -e

echo "[Startup] 1. 执行数据库自动迁移..."
api-monitor migrate

echo "[Startup] 2. 启动后台定时巡检 Worker..."
api-monitor worker &

echo "[Startup] 3. 启动前台 Web 控制台与 API..."
exec api-monitor api
