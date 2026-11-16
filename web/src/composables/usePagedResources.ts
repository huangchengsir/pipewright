import {computed,ref,toValue,watch,type MaybeRefOrGetter} from 'vue'
// Keep resource decision subscriptions bounded by the visible page.
export function usePagedResources<T>(source:MaybeRefOrGetter<readonly T[]>,size=25){
 const page=ref(1)
 const pages=computed(()=>Math.max(1,Math.ceil(toValue(source).length/size)))
 watch(pages,max=>{page.value=Math.min(page.value,max)})
 const items=computed(()=>toValue(source).slice((page.value-1)*size,page.value*size))
 function select(value:number){page.value=Math.max(1,Math.min(pages.value,Math.floor(value)))}
 return {page,pages,items,select}
}

export function includeSelected<T extends {id:string}>(page:readonly T[],all:readonly T[],ids:readonly string[]):T[]{
 const selected=new Set(ids.filter(Boolean)),seen=new Set(page.map(item=>item.id))
 return [...page,...all.filter(item=>selected.has(item.id)&&!seen.has(item.id))]
}
