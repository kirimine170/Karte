export namespace main {
	
	export class ASRStatus {
	    initialized: boolean;
	    initializing: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ASRStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.initialized = source["initialized"];
	        this.initializing = source["initializing"];
	    }
	}
	export class FileItem {
	    path: string;
	    title: string;
	
	    static createFrom(source: any = {}) {
	        return new FileItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.title = source["title"];
	    }
	}
	export class GraphMeta {
	    directed: boolean;
	
	    static createFrom(source: any = {}) {
	        return new GraphMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directed = source["directed"];
	    }
	}
	export class GraphEdge {
	    id: string;
	    source: string;
	    target: string;
	    kind: string;
	    weight: number;
	    targetHash?: string;
	    sourceHash?: string;
	    linkVersion?: number;
	    targetUpdated?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new GraphEdge(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.source = source["source"];
	        this.target = source["target"];
	        this.kind = source["kind"];
	        this.weight = source["weight"];
	        this.targetHash = source["targetHash"];
	        this.sourceHash = source["sourceHash"];
	        this.linkVersion = source["linkVersion"];
	        this.targetUpdated = source["targetUpdated"];
	    }
	}
	export class GraphNode {
	    id: string;
	    label: string;
	    kind: string;
	    exists: boolean;
	    degIn: number;
	    degOut: number;
	    tags: string[];
	    hash?: string;
	
	    static createFrom(source: any = {}) {
	        return new GraphNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.kind = source["kind"];
	        this.exists = source["exists"];
	        this.degIn = source["degIn"];
	        this.degOut = source["degOut"];
	        this.tags = source["tags"];
	        this.hash = source["hash"];
	    }
	}
	export class GraphData {
	    nodes: GraphNode[];
	    edges: GraphEdge[];
	    meta: GraphMeta;
	
	    static createFrom(source: any = {}) {
	        return new GraphData(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.nodes = this.convertValues(source["nodes"], GraphNode);
	        this.edges = this.convertValues(source["edges"], GraphEdge);
	        this.meta = this.convertValues(source["meta"], GraphMeta);
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

export namespace sync {
	
	export class Peer {
	    id: string;
	    name: string;
	    address: string;
	    port: number;
	    // Go type: time
	    last_seen: any;
	    Conn: any;
	
	    static createFrom(source: any = {}) {
	        return new Peer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.address = source["address"];
	        this.port = source["port"];
	        this.last_seen = this.convertValues(source["last_seen"], null);
	        this.Conn = source["Conn"];
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

