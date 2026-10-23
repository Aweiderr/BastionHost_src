export namespace coreapi {
	
	export class Account {
	    account_id: number;
	    account_name: string;
	    account_type: string;
	    privileged: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Account(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.account_id = source["account_id"];
	        this.account_name = source["account_name"];
	        this.account_type = source["account_type"];
	        this.privileged = source["privileged"];
	    }
	}
	export class Asset {
	    asset_id: number;
	    asset_name: string;
	    host: string;
	    port: number;
	    protocol: string;
	    level: string;
	    os_type: string;
	    remark: string;
	    accounts: Account[];
	    grant_source: string;
	    valid_to?: string;
	    can_file: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Asset(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.asset_id = source["asset_id"];
	        this.asset_name = source["asset_name"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.protocol = source["protocol"];
	        this.level = source["level"];
	        this.os_type = source["os_type"];
	        this.remark = source["remark"];
	        this.accounts = this.convertValues(source["accounts"], Account);
	        this.grant_source = source["grant_source"];
	        this.valid_to = source["valid_to"];
	        this.can_file = source["can_file"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class LoginResult {
	    access_token: string;
	    refresh_token: string;
	    user_id: number;
	    username: string;
	    display_name: string;
	    roles: string[];
	    permissions: string[];
	    mfa_enabled: boolean;
	    must_change_password: boolean;
	
	    static createFrom(source: any = {}) {
	        return new LoginResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.access_token = source["access_token"];
	        this.refresh_token = source["refresh_token"];
	        this.user_id = source["user_id"];
	        this.username = source["username"];
	        this.display_name = source["display_name"];
	        this.roles = source["roles"];
	        this.permissions = source["permissions"];
	        this.mfa_enabled = source["mfa_enabled"];
	        this.must_change_password = source["must_change_password"];
	    }
	}
	export class SessionInfo {
	    session_id: number;
	    session_no: string;
	    asset_name: string;
	    account_name: string;
	    client_type: string;
	    status: string;
	    start_time: string;
	    command_count: number;
	    block_count: number;
	
	    static createFrom(source: any = {}) {
	        return new SessionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.session_id = source["session_id"];
	        this.session_no = source["session_no"];
	        this.asset_name = source["asset_name"];
	        this.account_name = source["account_name"];
	        this.client_type = source["client_type"];
	        this.status = source["status"];
	        this.start_time = source["start_time"];
	        this.command_count = source["command_count"];
	        this.block_count = source["block_count"];
	    }
	}

}

export namespace device {
	
	export class Info {
	    hostname: string;
	    os: string;
	    arch: string;
	    mac: string;
	    disk_serial: string;
	    board_serial: string;
	    cpu: string;
	    device_id: string;
	    collected_at: string;
	    warnings?: string[];
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hostname = source["hostname"];
	        this.os = source["os"];
	        this.arch = source["arch"];
	        this.mac = source["mac"];
	        this.disk_serial = source["disk_serial"];
	        this.board_serial = source["board_serial"];
	        this.cpu = source["cpu"];
	        this.device_id = source["device_id"];
	        this.collected_at = source["collected_at"];
	        this.warnings = source["warnings"];
	    }
	}

}

export namespace main {
	
	export class SessionBrief {
	    key: string;
	    asset_id: number;
	    asset: string;
	    account: string;
	    gateway: string;
	    login: string;
	    opened_at: string;
	
	    static createFrom(source: any = {}) {
	        return new SessionBrief(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.asset_id = source["asset_id"];
	        this.asset = source["asset"];
	        this.account = source["account"];
	        this.gateway = source["gateway"];
	        this.login = source["login"];
	        this.opened_at = source["opened_at"];
	    }
	}

}

