box.cfg{
    listen = '0.0.0.0:3301',
    wal_mode = 'none',
    memtx_memory = 256 * 1024 * 1024,
    log_level = 5,
    too_long_threshold = 0.5 -- Добавляем порог для длинных запросов
}

-- HTTP сервер для healthcheck
local http = require('http.server').new('0.0.0.0', 8081)
http:route({path = '/'}, function()
    return {status = 200, body = 'OK'}
end)
http:start()

-- Создаем пространство
if not box.space.users then
    box.schema.space.create('users', {
        format = {
            {name = 'id', type = 'unsigned'},
            {name = 'name', type = 'string'}
        }
    })
    box.space.users:create_index('primary', {parts = {'id'}})
end

-- Логирование статуса
require('log').info("Tarantool initialized successfully")

-- Оставляем процесс работать
while true do
    require('fiber').sleep(1)
end