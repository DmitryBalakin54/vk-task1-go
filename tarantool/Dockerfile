FROM tarantool/tarantool:2.10

# Устанавливаем curl для healthcheck
RUN apk add --no-cache curl

# Копируем файл инициализации
COPY init.lua /opt/tarantool/

# Запускаем Tarantool
CMD ["tarantool", "/opt/tarantool/init.lua"]