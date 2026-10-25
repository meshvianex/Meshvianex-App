    state.detail.drafts=state.detail.drafts||{};
    const detailDraftKey=(realmID,component)=>realmID+'|'+component.type+'|'+(component.instance_id||'');
    const originalRenderDetailInspector=renderDetailInspector;
    renderDetailInspector=function(realm,component,components){
      const key=detailDraftKey(realm.id,component),draft=state.detail.drafts[key];
      if(draft&&draft.plugin===component.plugin){delete state.detail.drafts[key]}
      else if(draft)component=Object.assign({},component,{plugin:draft.plugin,config:cloneConfig(draft.config)});
      originalRenderDetailInspector(realm,component,components)
    };
    document.addEventListener('change',event=>{
      if(event.target?.id!=='detail-plugin')return;
      const realm=realmById(state.detail.realmId);if(!realm)return;
      const component=componentInstances(realm).find(item=>item.type+':'+item.instance_id===state.detail.selected)||componentModel(realm).find(item=>item.type===state.detail.selected);if(!component)return;
      state.detail.drafts[detailDraftKey(realm.id,component)]={plugin:event.target.value,config:{}}
    },true)
  
