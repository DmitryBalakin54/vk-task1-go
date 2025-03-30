local log = require('log')

log.info("CUSTOM INIT STARTED")

box.cfg{
    listen = '0.0.0.0:3301'
}

log.info("Custom initialization started")

if not box.schema.user.exists('admin') then
    box.schema.user.create('admin', { password = 'password123' })
end

box.schema.user.grant('admin', 'super', nil, nil, {if_not_exists = true})
box.schema.user.grant('guest', 'read,write,execute', 'universe', nil, {if_not_exists = true})

if not box.sequence.poll_id_seq then
    log.info("Creating poll_id_seq sequence")
    box.schema.sequence.create('poll_id_seq', { start = 1, step = 1 })
else
    log.info("poll_id_seq already exists")
end

if not box.space.polls then
    log.info("Creating space 'polls'")

    box.schema.space.create('polls', {
        format = {
            {name = 'id', type = 'unsigned'},
            {name = 'question', type = 'string'},
            {name = 'options', type = 'array'},
            {name = 'votes', type = 'map'},
            {name = 'voters', type = 'map'},
            {name = 'creator_id', type = 'string'},
            {name = 'channel_id', type = 'string'},
            {name = 'active', type = 'boolean'},
            {name = 'created_at', type = 'number'},
            {name = 'updated_at', type = 'number'}
        }
    })

    box.space.polls:create_index('primary', {parts = {'id'}, type = 'tree'})
    box.space.polls:create_index('channel', {parts = {'channel_id', 'created_at'}, type = 'tree'})
    box.space.polls:create_index('creator', {parts = {'creator_id', 'created_at'}, type = 'tree'})

    log.info("Space 'polls' created")
else
    log.info("Space 'polls' already exists")
end

function create_poll(creator_id, channel_id, question, options)
    if type(question) ~= 'string' or #question < 3 then
        return nil, "Question must be a string (minimum 3 characters)"
    end

    if type(options) ~= 'table' or #options < 2 then
        return nil, "Minimum 2 options required"
    end

    local new_id = box.sequence.poll_id_seq:next()
    log.info("Generated poll ID: " .. tostring(new_id))

    local now = os.time()

    local votes = setmetatable({}, {__serialize = 'map'})
    for i = 1, #options do
        votes[tostring(i)] = 0
    end

    local voters = setmetatable({}, {__serialize = 'map'})

    local new_poll = {
        new_id, question, options, votes, voters,
        creator_id, channel_id, true, now, now
    }

    local ok, err = pcall(function()
        box.space.polls:insert(new_poll)
    end)

    if not ok then
        log.error("Database error: " .. tostring(err))
        return nil, "Database error: " .. tostring(err)
    end

    return new_id
end

log.info("Function 'create_poll' defined")

if not box.func.create_poll then
    box.schema.func.create('create_poll')
    box.schema.user.grant('admin', 'execute', 'function', 'create_poll')
    log.info("Function 'create_poll' registered successfully")
else
    log.info("Function 'create_poll' already exists")
end

function get_poll_by_id(poll_id)
    local poll = box.space.polls:get(poll_id)
    if not poll then
        return nil, "Poll not found"
    end

    return poll
end

function get_poll(poll_id, channel_id)
    local poll = box.space.polls:get(poll_id)

    if not poll then
        return nil, "Poll not found"
    end

    if channel_id ~= poll.channel_id then
        return nil, "Poll not found"
    end

    return poll
end

if not box.func.get_poll_by_id then
    box.schema.func.create('get_poll_by_id')
    box.schema.user.grant('admin', 'execute', 'function', 'get_poll_by_id')
end

if not box.func.get_poll then
    box.schema.func.create('get_poll')
    box.schema.user.grant('admin', 'execute', 'function', 'get_poll')
end

function vote(poll_id, user_id, option_index, channel_id)
    local poll = box.space.polls:get(poll_id)
    if not poll then
        return nil, "Poll not found"
    end

    if poll.channel_id ~= channel_id then
        return nil, "Poll not found"
    end

    if not poll.active then
        return nil, "Poll is closed"
    end
    if poll.voters[user_id] then
        return nil, "You have already voted"
    end

    if option_index < 1 or option_index > #poll.options then
        return nil, "Invalid option"
    end

    local new_votes = table.deepcopy(poll.votes or {})
    local new_voters = table.deepcopy(poll.voters or {})
    local option_str = tostring(option_index)

    new_votes[option_str] = (new_votes[option_str] or 0) + 1
    new_voters[user_id] = true

    local res = box.space.polls:update(poll_id, {
        {'=', 'votes', new_votes},
        {'=', 'voters', new_voters},
        {'=', 'updated_at', os.time()}
    })

    if res ~= nil then
        return true
    else
        return false, "Failed to update poll"
    end
end

if not box.func.vote then
    box.schema.func.create('vote')
    box.schema.user.grant('admin', 'execute', 'function', 'vote')
end

function close_poll(poll_id, user_id, channel_id)
    local poll = box.space.polls:get(poll_id)
    if not poll then
        return nil, "Poll not found"
    end

    if poll.channel_id ~= channel_id then
        return nil, "Poll not found"
    end

    if poll.creator_id ~= user_id then
        return nil, "Only creator can close the poll"
    end

    if not poll.active then
        return nil, "Poll is already closed"
    end

    local res = box.space.polls:update(poll_id, {
        {'=', 'active', false},
        {'=', 'updated_at', os.time()}
    })

    if res ~= nil then
        return true
    else
        return false, "Failed to close poll"
    end
end

if not box.func.close_poll then
    box.schema.func.create('close_poll')
    box.schema.user.grant('admin', 'execute', 'function', 'close_poll')
end

function delete_poll(poll_id, user_id, channel_id)
    local poll = box.space.polls:get(poll_id)
    if not poll then
        return nil, "Poll not found"
    end

    if poll.channel_id ~= channel_id then
        return nil, "Poll not found"
    end

    if poll.creator_id ~= user_id then
        return nil, "Only creator can delete the poll"
    end

    local ok, err = pcall(function()
        return box.space.polls:delete(poll_id)
    end)

    if not ok then
        return nil, "Database error: " .. tostring(err)
    end

    if not err then
        return nil, "Failed to delete poll"
    end

    return true
end

if not box.func.delete_poll then
    box.schema.func.create('delete_poll')
    box.schema.user.grant('admin', 'execute', 'function', 'delete_poll')
end

log.info("Tarantool initialization completed")
log.info("Box status: " .. box.info.status)
log.info("Admin user exists: " .. tostring(box.schema.user.exists('admin')))
log.info("Polls space exists: " .. tostring(box.space.polls ~= nil))
