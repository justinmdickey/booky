--[[
Booky stats sync — a KOReader plugin that uploads the reading statistics
database (statistics.sqlite3) to a self-hosted Booky server over WiFi.

It pairs with the Booky server's POST /api/stats/upload endpoint. Upload happens:
  * manually from the menu,
  * automatically when KOReader suspends / closes a document (throttled), and
  * (optionally) when you start reading.

Install: copy this folder to .adds/koreader/plugins/booky.koplugin on the Kobo,
then configure the server URL under Tools (gear) -> Booky stats sync.
]]

local DataStorage = require("datastorage")
local Dispatcher = require("dispatcher")
local InfoMessage = require("ui/widget/infomessage")
local InputDialog = require("ui/widget/inputdialog")
local LuaSettings = require("luasettings")
local NetworkMgr = require("ui/network/manager")
local UIManager = require("ui/uimanager")
local WidgetContainer = require("ui/widget/container/widgetcontainer")
local lfs = require("libs/libkoreader-lfs")
local logger = require("logger")
local _ = require("gettext")
local T = require("ffi/util").template

local Booky = WidgetContainer:extend{
    name = "booky",
    is_doc_only = false,
}

local STATS_DB = DataStorage:getSettingsDir() .. "/statistics.sqlite3"
local MIN_INTERVAL = 60 * 30 -- throttle auto-uploads to once per 30 min

function Booky:init()
    self.settings = LuaSettings:open(DataStorage:getSettingsDir() .. "/booky.lua")
    self.server_url = self.settings:readSetting("server_url") or ""
    self.username = self.settings:readSetting("username") or ""
    self.password = self.settings:readSetting("password") or ""
    self.auto_upload = self.settings:nilOrTrue("auto_upload")
    self.last_upload = self.settings:readSetting("last_upload") or 0
    self.download_dir = self.settings:readSetting("download_dir") or self:defaultDownloadDir()
    self.auto_sync_books = self.settings:isTrue("auto_sync_books")
    self.last_book_sync = self.settings:readSetting("last_book_sync") or 0
    self:onDispatcherRegisterActions()
    self.ui.menu:registerToMainMenu(self)
end

-- defaultDownloadDir picks a sensible place to drop synced books: KOReader's
-- configured home/last directory, falling back to the Kobo's onboard root.
function Booky:defaultDownloadDir()
    local home = G_reader_settings and G_reader_settings:readSetting("home_dir")
    if home and lfs.attributes(home, "mode") == "directory" then
        return home .. "/Booky"
    end
    if lfs.attributes("/mnt/onboard", "mode") == "directory" then
        return "/mnt/onboard/Booky"
    end
    return DataStorage:getDataDir() .. "/Booky"
end

function Booky:onDispatcherRegisterActions()
    Dispatcher:registerAction("booky_upload_stats", {
        category = "none",
        event = "BookyUploadStats",
        title = _("Booky: upload reading stats"),
        general = true,
    })
    Dispatcher:registerAction("booky_sync_books", {
        category = "none",
        event = "BookySyncBooks",
        title = _("Booky: sync all books"),
        general = true,
    })
end

function Booky:addToMainMenu(menu_items)
    menu_items.booky = {
        text = _("Booky stats sync"),
        sorting_hint = "tools",
        sub_item_table = {
            {
                text = _("Sync all books now"),
                keep_menu_open = true,
                callback = function() self:syncBooks(true) end,
            },
            {
                text = _("Auto-sync books on WiFi connect"),
                checked_func = function() return self.auto_sync_books end,
                callback = function()
                    self.auto_sync_books = not self.auto_sync_books
                    self.settings:saveSetting("auto_sync_books", self.auto_sync_books)
                    self.settings:flush()
                end,
            },
            {
                text_func = function()
                    return T(_("Download folder: %1"), self.download_dir)
                end,
                keep_menu_open = true,
                callback = function()
                    self:editSetting("download_dir", _("Book download folder"),
                        self:defaultDownloadDir())
                end,
            },
            {
                text = _("Upload stats now"),
                keep_menu_open = true,
                separator = true,
                callback = function() self:upload(true) end,
            },
            {
                text = _("Auto-upload on close/suspend"),
                checked_func = function() return self.auto_upload end,
                callback = function()
                    self.auto_upload = not self.auto_upload
                    self.settings:saveSetting("auto_upload", self.auto_upload)
                    self.settings:flush()
                end,
            },
            {
                text = _("Set server URL"),
                keep_menu_open = true,
                callback = function() self:editSetting("server_url", _("Booky server URL"),
                    _("e.g. http://192.168.1.10:8222")) end,
            },
            {
                text = _("Set username (optional)"),
                keep_menu_open = true,
                callback = function() self:editSetting("username", _("Booky username"), "") end,
            },
            {
                text = _("Set password (optional)"),
                keep_menu_open = true,
                callback = function() self:editSetting("password", _("Booky password"), "") end,
            },
            {
                text_func = function()
                    if self.last_upload == 0 then return _("Last stats upload: never") end
                    return T(_("Last stats upload: %1"), os.date("%Y-%m-%d %H:%M", self.last_upload))
                end,
                enabled = false,
            },
            {
                text_func = function()
                    if self.last_book_sync == 0 then return _("Last book sync: never") end
                    return T(_("Last book sync: %1"), os.date("%Y-%m-%d %H:%M", self.last_book_sync))
                end,
                enabled = false,
            },
            {
                -- self.version is merged in from _meta.lua by KOReader's
                -- PluginLoader, so the version lives in exactly one place.
                text = T(_("Plugin version: %1"), self.version or "unknown"),
                enabled = false,
                separator = true,
            },
        },
    }
end

function Booky:editSetting(key, title, hint)
    local dialog
    dialog = InputDialog:new{
        title = title,
        input = self[key] or "",
        input_hint = hint,
        buttons = {{
            { text = _("Cancel"), id = "close", callback = function() UIManager:close(dialog) end },
            { text = _("Save"), is_enter_default = true, callback = function()
                local v = dialog:getInputText()
                self[key] = v
                self.settings:saveSetting(key, v)
                self.settings:flush()
                UIManager:close(dialog)
            end },
        }},
    }
    UIManager:show(dialog)
    dialog:onShowKeyboard()
end

-- Public dispatcher events (bindable to gestures).
function Booky:onBookyUploadStats()
    self:upload(true)
    return true
end

function Booky:onBookySyncBooks()
    self:syncBooks(true)
    return true
end

-- Auto-sync books when WiFi comes up (throttled), if enabled.
function Booky:onNetworkConnected()
    if not self.auto_sync_books then return end
    if self.server_url == "" then return end
    local now = os.time()
    if now - self.last_book_sync < MIN_INTERVAL then return end
    self:syncBooks(false)
end

-- Auto-upload hooks. Reading position itself syncs via the stock kosync plugin;
-- here we ship the statistics DB so the Booky dashboard stays current.
function Booky:onSuspend() self:maybeAutoUpload() end
function Booky:onClose() self:maybeAutoUpload() end
function Booky:onCloseDocument() self:maybeAutoUpload() end

function Booky:maybeAutoUpload()
    if not self.auto_upload then return end
    if self.server_url == "" then return end
    local now = os.time()
    if now - self.last_upload < MIN_INTERVAL then return end
    self:upload(false)
end

function Booky:upload(verbose)
    if self.server_url == "" then
        if verbose then
            UIManager:show(InfoMessage:new{ text = _("Set your Booky server URL first.") })
        end
        return
    end
    if lfs.attributes(STATS_DB, "mode") ~= "file" then
        if verbose then
            UIManager:show(InfoMessage:new{ text = _("No statistics database found yet.") })
        end
        return
    end

    -- Ensure WiFi, then upload. NetworkMgr handles the e-ink "turn on WiFi" flow.
    NetworkMgr:runWhenOnline(function()
        self:doUpload(verbose)
    end)
end

function Booky:doUpload(verbose)
    local http = require("socket.http")
    local ltn12 = require("ltn12")
    local mime = require("mime")

    local f = io.open(STATS_DB, "rb")
    if not f then return end
    local data = f:read("*a")
    f:close()

    local url = self.server_url:gsub("/+$", "") .. "/api/stats/upload"
    local headers = {
        ["Content-Type"] = "application/octet-stream",
        ["Content-Length"] = tostring(#data),
    }
    if self.username ~= "" then
        local auth = mime.b64(self.username .. ":" .. self.password)
        headers["Authorization"] = "Basic " .. auth
    end

    -- http.request (table form) returns: 1, status_code, headers, status_line
    -- (or nil, error_string on a transport/connection failure).
    local respbody = {}
    local result, code = http.request{
        url = url,
        method = "POST",
        headers = headers,
        source = ltn12.source.string(data),
        sink = ltn12.sink.table(respbody),
    }
    -- On a connection-level failure result is nil and `code` holds an error
    -- string; on an HTTP response result is 1 and `code` is the numeric status.
    local status = tonumber(code)

    if result and status == 200 then
        self.last_upload = os.time()
        self.settings:saveSetting("last_upload", self.last_upload)
        self.settings:flush()
        local summary = self:summarizeResponse(respbody)
        logger.info("Booky: stats uploaded OK", summary)
        if verbose then
            UIManager:show(InfoMessage:new{
                text = T(_("Reading stats uploaded to Booky.\n%1"), summary),
                timeout = 3,
            })
        end
    else
        local msg
        if not result then
            -- Transport error: no HTTP response (DNS, refused, timeout, TLS…).
            msg = T(_("Booky upload failed: %1\n\nCheck the server URL and that you're on the same network."),
                tostring(code))
        elseif status == 401 then
            msg = _("Booky upload failed: 401 Unauthorized.\n\nCheck the username and password.")
        elseif status then
            msg = T(_("Booky upload failed: HTTP %1.\n%2"), status, self:summarizeResponse(respbody))
        else
            msg = T(_("Booky upload failed: %1"), tostring(code))
        end
        logger.warn("Booky: upload failed", code, status)
        if verbose then
            UIManager:show(InfoMessage:new{ text = msg })
        end
    end
end

-- summarizeResponse renders a short, human-readable line from the server's JSON
-- response body (it reports books/page_stats counts on success).
function Booky:summarizeResponse(respbody)
    local body = table.concat(respbody or {})
    if body == "" then return _("Done.") end
    local books = body:match('"books"%s*:%s*(%d+)')
    local pages = body:match('"page_stats"%s*:%s*(%d+)')
    if books and pages then
        return T(_("%1 books, %2 page stats synced."), books, pages)
    end
    -- Fall back to the raw body, trimmed, so we never print a Lua table address.
    return (body:gsub("%s+", " ")):sub(1, 200)
end

--[[ ---------------------------------------------------------------------------
Book sync: pull the full library manifest from Booky and download any books not
already present in the download folder. Incremental — books already on the
device (matched by filename) are skipped.
----------------------------------------------------------------------------]]

function Booky:syncBooks(verbose)
    if self.server_url == "" then
        if verbose then
            UIManager:show(InfoMessage:new{ text = _("Set your Booky server URL first.") })
        end
        return
    end
    NetworkMgr:runWhenOnline(function()
        -- Run inside a Trapper coroutine so we can show live progress and let
        -- the user dismiss it; downloads are blocking socket calls.
        local Trapper = require("ui/trapper")
        Trapper:wrap(function() self:doSyncBooks(verbose) end)
    end)
end

function Booky:authHeaders(extra)
    local mime = require("mime")
    local headers = extra or {}
    if self.username ~= "" then
        headers["Authorization"] = "Basic " .. mime.b64(self.username .. ":" .. self.password)
    end
    return headers
end

function Booky:fetchManifest()
    local http = require("socket.http")
    local ltn12 = require("ltn12")
    local body = {}
    local url = self.server_url:gsub("/+$", "") .. "/api/sync/manifest"
    local result, code = http.request{
        url = url,
        method = "GET",
        headers = self:authHeaders(),
        sink = ltn12.sink.table(body),
    }
    local status = tonumber(code)
    if not result then
        return nil, T(_("Couldn't reach Booky: %1"), tostring(code))
    end
    if status == 401 then
        return nil, _("401 Unauthorized — check the username and password.")
    end
    if status ~= 200 then
        return nil, T(_("Manifest request failed: HTTP %1."), status)
    end
    local JSON = require("json")
    local ok, parsed = pcall(JSON.decode, table.concat(body))
    if not ok or type(parsed) ~= "table" or type(parsed.books) ~= "table" then
        return nil, _("Couldn't read the library manifest from Booky.")
    end
    return parsed.books
end

-- partialMD5 reproduces KOReader's util.partialMD5: twelve 1 KiB samples at
-- offsets 1024 * 4^i for i = -1..10. This is the same content fingerprint the
-- Booky server reports in the manifest, so we can dedupe by content regardless
-- of filename or folder layout.
function Booky:partialMD5(path)
    local md5 = require("ffi/sha2").md5
    local f = io.open(path, "rb")
    if not f then return nil end
    local step, size = 1024, 1024
    local update = md5()
    for i = -1, 10 do
        local shift = 2 * i
        local offset = (shift >= 0) and (step * (2 ^ shift)) or (step / (2 ^ (-shift)))
        f:seek("set", math.floor(offset))
        local sample = f:read(size)
        if not sample then break end
        update(sample)
    end
    f:close()
    return update()
end

-- scanLocalHashes walks the download folder (recursively) and returns a set of
-- partial-MD5 hashes for every .epub/.kepub/.pdf/.mobi/.azw3/.cbz it finds, so
-- books already on the device — under any name or subfolder — are recognised.
function Booky:scanLocalHashes(Trapper)
    local hashes = {}
    local exts = { epub=true, kepub=true, pdf=true, mobi=true, azw3=true, cbz=true, fb2=true }
    local stack = { self.download_dir }
    local n = 0
    while #stack > 0 do
        local dir = table.remove(stack)
        for entry in lfs.dir(dir) do
            if entry ~= "." and entry ~= ".." then
                local p = dir .. "/" .. entry
                local mode = lfs.attributes(p, "mode")
                if mode == "directory" then
                    table.insert(stack, p)
                elseif mode == "file" then
                    local ext = entry:match("%.([^.]+)$")
                    if ext and exts[ext:lower()] then
                        local h = self:partialMD5(p)
                        if h then
                            hashes[h] = true
                            n = n + 1
                            if Trapper and n % 10 == 0 then
                                Trapper:info(T(_("Booky: scanning existing books… (%1)"), n))
                            end
                        end
                    end
                end
            end
        end
    end
    return hashes
end

function Booky:doSyncBooks(verbose)
    local Trapper = require("ui/trapper")

    Trapper:info(_("Booky: fetching library…"))
    local books, err = self:fetchManifest()
    if not books then
        Trapper:clear()
        UIManager:show(InfoMessage:new{ text = T(_("Book sync failed.\n%1"), err) })
        return
    end

    -- Ensure download folder exists.
    if lfs.attributes(self.download_dir, "mode") ~= "directory" then
        lfs.mkdir(self.download_dir)
    end

    -- Build a content-hash set of everything already in the download folder so
    -- we skip books you already have, no matter their filename or subfolder.
    local local_hashes = self:scanLocalHashes(Trapper)

    local total = #books
    local downloaded, skipped = 0, 0
    local missing = {}    -- books whose file is gone from the library (404)
    local errored = {}    -- other download failures, with reason
    for i, b in ipairs(books) do
        local dest = self.download_dir .. "/" .. b.filename
        local have = (b.md5 and b.md5 ~= "" and local_hashes[b.md5])
            or lfs.attributes(dest, "mode") == "file"
        if have then
            skipped = skipped + 1
        else
            local keep_going = Trapper:info(T(_("Booky: downloading %1/%2\n%3"),
                i, total, b.title or b.filename))
            if not keep_going then -- user dismissed; stop cleanly
                break
            end
            local ok, reason = self:downloadBook(b, dest)
            if ok then
                downloaded = downloaded + 1
                if b.md5 and b.md5 ~= "" then local_hashes[b.md5] = true end
            elseif reason == "missing" then
                table.insert(missing, b.title or b.filename)
            else
                table.insert(errored, (b.title or b.filename) .. " (" .. tostring(reason) .. ")")
            end
        end
    end
    local failed = #missing + #errored

    self.last_book_sync = os.time()
    self.settings:saveSetting("last_book_sync", self.last_book_sync)
    self.settings:flush()

    Trapper:clear()
    logger.info("Booky: book sync done", downloaded, skipped, failed)

    -- Write the full failure detail to the log, where length doesn't matter and
    -- the device screen stays uncluttered.
    if #missing > 0 then
        logger.warn("Booky: " .. #missing .. " book(s) missing on server (file gone from library): "
            .. table.concat(missing, "; "))
    end
    if #errored > 0 then
        logger.warn("Booky: " .. #errored .. " book(s) had download errors: "
            .. table.concat(errored, "; "))
    end

    if verbose or downloaded > 0 or failed > 0 then
        local msg = T(_("Book sync complete.\n%1 new · %2 already had · %3 failed."),
            downloaded, skipped, failed)
        if downloaded > 0 then
            msg = msg .. "\n" .. T(_("Saved to: %1"), self.download_dir)
        end
        -- Keep the on-device popup short (e-ink screens don't scroll); explain
        -- the common case briefly and point at the log for the full list.
        if #missing > 0 then
            msg = msg .. "\n\n" .. T(_("%1 missing from the server — they're in your library catalog but the file is gone. Fix in Calibre/CWA."), #missing)
        end
        if #errored > 0 then
            msg = msg .. "\n\n" .. T(_("%1 had network/server errors."), #errored)
        end
        if failed > 0 then
            msg = msg .. "\n\n" .. T(_("Full list of failures is in the log:\n%1"), self:logPath())
        end
        UIManager:show(InfoMessage:new{ text = msg, timeout = (failed == 0 and downloaded == 0) and 3 or nil })
    end
    -- Refresh the file browser if it's showing the download folder.
    if self.ui and self.ui.file_chooser then
        self.ui.file_chooser:refreshPath()
    end
end

-- logPath returns where KOReader writes its log on this device, so the user
-- can find the full failure list. On a Kobo this is
-- .adds/koreader/crash.log on the visible USB partition.
function Booky:logPath()
    return DataStorage:getDataDir() .. "/crash.log"
end

-- downloadBook streams one book to a temp file then renames into place, so an
-- interrupted download never leaves a half-written file that looks "present".
-- Returns (true) on success, or (false, reason) where reason is "missing" for a
-- 404 (file gone from the library), an HTTP status number, or a transport error
-- string.
function Booky:downloadBook(b, dest)
    local http = require("socket.http")
    local ltn12 = require("ltn12")
    local url = self.server_url:gsub("/+$", "") .. b.url
    local tmp = dest .. ".part"
    local out = io.open(tmp, "wb")
    if not out then return false, _("can't write to download folder") end

    local result, code = http.request{
        url = url,
        method = "GET",
        headers = self:authHeaders(),
        sink = ltn12.sink.file(out), -- closes the file when done
    }
    local status = tonumber(code)
    if result and status == 200 then
        os.rename(tmp, dest)
        return true
    end
    os.remove(tmp)
    logger.warn("Booky: download failed", b.filename, code, status)
    if status == 404 then
        return false, "missing"
    elseif status == 401 then
        return false, _("401 unauthorized")
    elseif status then
        return false, T(_("HTTP %1"), status)
    end
    return false, tostring(code) -- transport error string
end

return Booky
