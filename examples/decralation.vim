let foo = 42
let [foo,_unused0,baz] = [1,2,3]
let [foo,_unused1,_unused2,baz] = [1,2,3,4]
let [foo,_unused3,baz,_unused4] = [1,2,3,4]
let [_unused1] = [1]
function! s:name() abort
  let foo = 42
  let [foo,_unused0,baz] = [1,2,3]
  let [foo,_unused1,_unused2,baz] = [1,2,3,4]
  let [foo,_unused3,baz,_unused4] = [1,2,3,4]
  let [_unused1] = [1]
  function! s:inner() abort
    let foo = 42
    let [foo,_unused0,baz] = [1,2,3]
    let [foo,_unused1,_unused2,baz] = [1,2,3,4]
    let [foo,_unused3,baz,_unused4] = [1,2,3,4]
    let [_unused1] = [1]
  endfunction
endfunction