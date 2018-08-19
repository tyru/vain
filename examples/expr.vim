scriptencoding utf-8

let n = 42

let i = let j = 0
1 ? 2 : 3
1 ? "12" : "34"
1 && 2
1 || 2
1 ==# 2
1 ==? 2
1 !=# 2
1 !=? 2
1 ># 2
1 >? 2
1 >=# 2
1 >=? 2
1 <# 2
1 <? 2
1 <=# 2
1 <=? 2
1 =~# 2
1 =~? 2
1 !~# 2
1 !~? 2
1 is# 2
1 is? 2
1 isnot# 2
1 isnot? 2
1 + 2
1 - 2
1 * 2
1 / 2
1 % 2
!1
-1
+1
[]
[1]
[1,2]
[1,2]
[1,[2,[3]]]
{}
{'key':'value'}
let foo = {}
foo.bar
foo.from
let bar = ''
foo[bar]
foo["bar"]
call foo()
call foo.bar()
call foo.from()
call foo[bar]()
call foo["bar"]()
call foo.bar.baz()
call foo.bar.baz(42,"hello",[123],{'key':42})
let arr = []
arr[0:1]
arr[null:1]
arr[0:null]
let [begin,end] = [1,2]
arr[begin:end]
arr[null:end]
arr[begin:null]