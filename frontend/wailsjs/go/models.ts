export namespace app {
	
	export class DraftView {
	    threadId: number;
	    textRu: string;
	    textEn: string;
	    backRu: string;
	
	    static createFrom(source: any = {}) {
	        return new DraftView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.threadId = source["threadId"];
	        this.textRu = source["textRu"];
	        this.textEn = source["textEn"];
	        this.backRu = source["backRu"];
	    }
	}
	export class ReactionView {
	    name: string;
	    count: number;
	
	    static createFrom(source: any = {}) {
	        return new ReactionView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.count = source["count"];
	    }
	}
	export class MessageView {
	    id: number;
	    ts: string;
	    time: string;
	    author: string;
	    authorId: string;
	    isBot: boolean;
	    text: string;
	    textRu: string;
	    blocks: slackapi.Block[];
	    blocksRu: slackapi.Block[];
	    edited: boolean;
	    deleted: boolean;
	    reactions: ReactionView[];
	
	    static createFrom(source: any = {}) {
	        return new MessageView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.ts = source["ts"];
	        this.time = source["time"];
	        this.author = source["author"];
	        this.authorId = source["authorId"];
	        this.isBot = source["isBot"];
	        this.text = source["text"];
	        this.textRu = source["textRu"];
	        this.blocks = this.convertValues(source["blocks"], slackapi.Block);
	        this.blocksRu = this.convertValues(source["blocksRu"], slackapi.Block);
	        this.edited = source["edited"];
	        this.deleted = source["deleted"];
	        this.reactions = this.convertValues(source["reactions"], ReactionView);
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
	
	export class RefreshOutcome {
	    threadId: number;
	    title: string;
	    changed: number;
	    error: string;
	
	    static createFrom(source: any = {}) {
	        return new RefreshOutcome(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.threadId = source["threadId"];
	        this.title = source["title"];
	        this.changed = source["changed"];
	        this.error = source["error"];
	    }
	}
	export class SentView {
	    threadId: number;
	    ts: string;
	    permalink: string;
	
	    static createFrom(source: any = {}) {
	        return new SentView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.threadId = source["threadId"];
	        this.ts = source["ts"];
	        this.permalink = source["permalink"];
	    }
	}
	export class ThreadItem {
	    id: number;
	    title: string;
	    channelId: string;
	    workspace: string;
	    permalink: string;
	    archived: boolean;
	    needsRefresh: boolean;
	    addedAt: string;
	    lastFetchedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new ThreadItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.channelId = source["channelId"];
	        this.workspace = source["workspace"];
	        this.permalink = source["permalink"];
	        this.archived = source["archived"];
	        this.needsRefresh = source["needsRefresh"];
	        this.addedAt = source["addedAt"];
	        this.lastFetchedAt = source["lastFetchedAt"];
	    }
	}
	export class ThreadView {
	    thread: ThreadItem;
	    summary: string;
	    summaryBlocks: slackapi.Block[];
	    messages: MessageView[];
	    draft: DraftView;
	
	    static createFrom(source: any = {}) {
	        return new ThreadView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.thread = this.convertValues(source["thread"], ThreadItem);
	        this.summary = source["summary"];
	        this.summaryBlocks = this.convertValues(source["summaryBlocks"], slackapi.Block);
	        this.messages = this.convertValues(source["messages"], MessageView);
	        this.draft = this.convertValues(source["draft"], DraftView);
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
	export class TokenStatus {
	    ok: boolean;
	    user: string;
	    team: string;
	    message: string;
	    fixable: boolean;
	
	    static createFrom(source: any = {}) {
	        return new TokenStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.user = source["user"];
	        this.team = source["team"];
	        this.message = source["message"];
	        this.fixable = source["fixable"];
	    }
	}

}

export namespace slackapi {
	
	export class Span {
	    kind: string;
	    text?: string;
	    url?: string;
	    id?: string;
	
	    static createFrom(source: any = {}) {
	        return new Span(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.text = source["text"];
	        this.url = source["url"];
	        this.id = source["id"];
	    }
	}
	export class Block {
	    kind: string;
	    lang?: string;
	    text?: string;
	    spans?: Span[];
	
	    static createFrom(source: any = {}) {
	        return new Block(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.lang = source["lang"];
	        this.text = source["text"];
	        this.spans = this.convertValues(source["spans"], Span);
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

