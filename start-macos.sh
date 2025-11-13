#!/bin/bash
# VMExporter Launcher for macOS
# Auto-detects your Mac architecture (Intel/Apple Silicon)

set -e

ARCH=$(uname -m)
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

echo "🍎 VMExporter для macOS"
echo "📍 Архитектура: $ARCH"
echo ""

# Detect binary
if [ "$ARCH" = "arm64" ]; then
    BINARY="$SCRIPT_DIR/dist/vmexporter-v1.0.0-darwin-arm64"
    echo "✅ Используется: Apple Silicon (M1/M2/M3)"
elif [ "$ARCH" = "x86_64" ]; then
    BINARY="$SCRIPT_DIR/dist/vmexporter-v1.0.0-darwin-amd64"
    echo "✅ Используется: Intel Mac"
else
    echo "❌ Неизвестная архитектура: $ARCH"
    exit 1
fi

# Check if binary exists
if [ ! -f "$BINARY" ]; then
    echo "❌ Бинарник не найден: $BINARY"
    echo ""
    echo "Запустите сборку:"
    echo "  make build-all"
    exit 1
fi

# Make executable if needed
chmod +x "$BINARY"

echo "🚀 Запускаю VMExporter..."
echo ""

# Launch with all arguments passed to script
exec "$BINARY" "$@"

