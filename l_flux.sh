#!/bin/bash

# Цветовая схема MiniOS
GREEN='\033[0;32m'; BRIGHT='\033[1;32m'; RED='\033[0;31m'; NC='\033[0m'; SPINNER="/-\|"

PROJECT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd "$PROJECT_DIR"
SESSION_FILE="$PROJECT_DIR/aria2.session"
BIN_NAME="flux2"

[ ! -f "$SESSION_FILE" ] && touch "$SESSION_FILE"

echo -ne "${GREEN}SYSTEM :: ${NC}ПОДГОТОВКА... "
pkill -SIGTERM aria2c 2>/dev/null
sleep 1
echo -e "${BRIGHT}[OK]${NC}"

# Запуск Aria2c с оптимизацией для отдачи
# Порт 6934 должен быть открыт на роутере
aria2c --enable-rpc=true \
--rpc-listen-all=true \
--input-file="$SESSION_FILE" \
--save-session="$SESSION_FILE" \
--force-save=true \
--save-session-interval=30 \
--continue=true \
--listen-port=6934 \
--enable-dht=true \
--bt-enable-lpd=true \
--enable-peer-exchange=true \
--bt-seed-unverified=true \
--seed-ratio=0 \
--daemon=true &>/dev/null

echo -ne "${GREEN}ENGINE :: ${NC}RPC ПОРТ 6800  "
for i in {1..20}; do
  if (timeout 0.2 bash -c "</dev/tcp/127.0.0.1/6800") &>/dev/null; then CONNECTED=true; break; fi
  echo -ne "\b${SPINNER:i%4:1}"; sleep 0.5
done

if [ "$CONNECTED" = true ]; then
    echo -e "\b${BRIGHT}[READY]${NC}"
    if [ ! -f "./$BIN_NAME" ]; then
        echo -e "${GREEN}BUILD  :: ${NC}СБОРКА v4.6..."
        CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o "$BIN_NAME" main.go
    fi
    clear
    ./"$BIN_NAME"
else
    echo -e "\b${RED}[FAILED]${NC}"; exit 1
fi

pkill -SIGTERM aria2c
echo -e "\n${BRIGHT}SAFE EXIT :: СЕССИЯ СОХРАНЕНА${NC}"