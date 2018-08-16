scriptencoding utf-8

let foo = 42
let bar = 1

if 42
  let foo = 123
  if 42
  let bar = 456
endif
endif
function! s:f() abort
  let [foo,_unused0] = [1,2]
  let [_unused1,bar,_unused2] = [1,2,3]
  let [_unused3,_unused4,baz] = [1,2,3]
  let [_unused1] = [1]
  while 42
    let foo = 123
    if 42
      let bar = 456
    endif
  endwhile
endfunction
function! s:g() abort
  let foo = 42
  function! s:inner() abort
    let foo = 42
    if 42
      let foo = 123
    endif
  endfunction
endfunction