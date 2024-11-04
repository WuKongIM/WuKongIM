

import { useCookies } from "vue3-cookies";
import API, { SystemSetting } from "./API";

const { cookies } = useCookies()

export default class App {

    public loginInfo = new LoginInfo()

    public _systemSetting = new SystemSetting(); // 系统设置
    public systemSettingLoaded = false; // 系统设置是否已经加载 

    // 单例
    private static _shard: App
    private constructor() {
        this.loginInfo.load()
    }
    static shard() {
        if (!App._shard) {
            App._shard = new App()
        }
        return App._shard
    }

    get systemSetting() {
        if (!this._systemSetting) {
            this.loadSystemSettingIfNeed()
        }
        return this._systemSetting || new SystemSetting()
    }

    loadSystemSettingIfNeed() {
        if (this.systemSettingLoaded) {
            return
        }
        API.shared.systemSettings().then((resp) => {
            this._systemSetting = resp
            this.systemSettingLoaded = true
        })
    }

}




export class LoginInfo {
    username!: string
    token!: string
    permissions: Permission[] = []


    public isLogin() {
        if (this.token && this.token != "") {
            return true
        }
        return false
    }

    load() {
        this.username = cookies.get('username') || ''
        this.token = cookies.get('token') || ''
        this.setPermissionFromStr(cookies.get('permissions') || '')
    }

    save() {
        cookies.set('username', this.username)
        cookies.set('token', this.token)
        cookies.set('permissions', this.formatPermissionToStr())
    }

    clear() {
        cookies.remove('username')
        cookies.remove('token')
        cookies.remove('permissions')
    }

    setPermissionFromStr(str: string) {
        if (!str) {
            return
        }
        str.split(',').forEach((item) => {
            let arr = item.split(':')
            if (arr.length >= 2) {
                let p = new Permission()
                p.resource = arr[0]
                p.action = arr[1]
                this.permissions.push(p)
            }
        })
    }

    formatPermissionToStr() {
        let str = ""
        if (!this.permissions) {
            return str
        }
        for (let i = 0; i < this.permissions.length; i++) {
            let p = this.permissions[i]
            if (i > 0) {
                str += ","
            }
            str += p.resource + ":" + p.action
        }
        return str
    }

    hasPermission(resource: string, action: string) {
        for (let i = 0; i < this.permissions.length; i++) {
            let p = this.permissions[i]
            if ((p.resource == resource || p.resource == All) && (p.action == action || p.action == All)) {
                return true
            }
        }
        return false
    }

}

export const All = "*"

export const ActionWrite = "write"
export const ActionRead = "read"

export class Permission {
    resource!: string
    action!: string
}