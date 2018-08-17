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
  let [__unused1] = [1]
  let __unused2 = 2
  while 42
    let [l,_unused5] = [123,456]
    let [_unused6,r] = [123,456]
    if 42
      let [l,_unused7] = [123,456]
      let [_unused8,r] = [123,456]
    endif
  endwhile
endfunction
function! s:g() abort
  let foo = 42
  function! s:inner() abort
    let foo = 42
    let [_unused0,bar,_unused1] = [1,2,3]
    while 42
      let [l,_unused2] = [123,456]
      let [_unused3,r] = [123,456]
      if 42
        let [l,_unused4] = [123,456]
        let [_unused5,r] = [123,456]
      endif
    endwhile
  endfunction
endfunction