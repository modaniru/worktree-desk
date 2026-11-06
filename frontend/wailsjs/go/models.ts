export namespace main {
	
	export class AddRequest {
	    repoPath: string;
	    mode: string;
	    name: string;
	    branch: string;
	    base: string;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new AddRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.repoPath = source["repoPath"];
	        this.mode = source["mode"];
	        this.name = source["name"];
	        this.branch = source["branch"];
	        this.base = source["base"];
	        this.path = source["path"];
	    }
	}
	export class Branch {
	    name: string;
	    remote: boolean;
	    inUse: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Branch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.remote = source["remote"];
	        this.inUse = source["inUse"];
	    }
	}
	export class RemoveRequest {
	    repoPath: string;
	    path: string;
	    branch: string;
	    force: boolean;
	    deleteBranch: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RemoveRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.repoPath = source["repoPath"];
	        this.path = source["path"];
	        this.branch = source["branch"];
	        this.force = source["force"];
	        this.deleteBranch = source["deleteBranch"];
	    }
	}
	export class Worktree {
	    path: string;
	    name: string;
	    pinned: boolean;
	    head: string;
	    branch: string;
	    detached: boolean;
	    bare: boolean;
	    locked: boolean;
	    prunable: boolean;
	    main: boolean;
	    exists: boolean;
	    dirty: number;
	    subject: string;
	    when: string;
	    updated: number;
	    upstream: string;
	    ahead: number;
	    behind: number;
	    gone: boolean;
	    baseRef: string;
	    baseBehind: number;
	    baseAhead: number;
	    conflicts: string[];
	    rebasing: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Worktree(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.pinned = source["pinned"];
	        this.head = source["head"];
	        this.branch = source["branch"];
	        this.detached = source["detached"];
	        this.bare = source["bare"];
	        this.locked = source["locked"];
	        this.prunable = source["prunable"];
	        this.main = source["main"];
	        this.exists = source["exists"];
	        this.dirty = source["dirty"];
	        this.subject = source["subject"];
	        this.when = source["when"];
	        this.updated = source["updated"];
	        this.upstream = source["upstream"];
	        this.ahead = source["ahead"];
	        this.behind = source["behind"];
	        this.gone = source["gone"];
	        this.baseRef = source["baseRef"];
	        this.baseBehind = source["baseBehind"];
	        this.baseAhead = source["baseAhead"];
	        this.conflicts = source["conflicts"];
	        this.rebasing = source["rebasing"];
	    }
	}
	export class Repo {
	    name: string;
	    path: string;
	    worktrees: Worktree[];
	    error?: string;
	    project: string;
	
	    static createFrom(source: any = {}) {
	        return new Repo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.worktrees = this.convertValues(source["worktrees"], Worktree);
	        this.error = source["error"];
	        this.project = source["project"];
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

}

