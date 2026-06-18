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
    self:onDispatcherRegisterActions()
    self.ui.menu:registerToMainMenu(self)
end

function Booky:onDispatcherRegisterActions()
    Dispatcher:registerAction("booky_upload_stats", {
        category = "none",
        event = "BookyUploadStats",
        title = _("Booky: upload reading stats"),
        general = true,
    })
end

function Booky:addToMainMenu(menu_items)
    menu_items.booky = {
        text = _("Booky stats sync"),
        sorting_hint = "tools",
        sub_item_table = {
            {
                text = _("Upload stats now"),
                keep_menu_open = true,
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
                    if self.last_upload == 0 then return _("Last upload: never") end
                    return T(_("Last upload: %1"), os.date("%Y-%m-%d %H:%M", self.last_upload))
                end,
                enabled = false,
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

-- Public dispatcher event.
function Booky:onBookyUploadStats()
    self:upload(true)
    return true
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
    local socket = require("socket")
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

    local respbody = {}
    local ok, code = socket.skip(1, http.request{
        url = url,
        method = "POST",
        headers = headers,
        source = ltn12.source.string(data),
        sink = ltn12.sink.table(respbody),
    })

    if ok and code == 200 then
        self.last_upload = os.time()
        self.settings:saveSetting("last_upload", self.last_upload)
        self.settings:flush()
        logger.info("Booky: stats uploaded OK")
        if verbose then
            UIManager:show(InfoMessage:new{
                text = T(_("Reading stats uploaded to Booky.\n%1"), table.concat(respbody)),
                timeout = 3,
            })
        end
    else
        logger.warn("Booky: upload failed", code)
        if verbose then
            UIManager:show(InfoMessage:new{
                text = T(_("Booky upload failed (%1)."), tostring(code)),
            })
        end
    end
end

return Booky
