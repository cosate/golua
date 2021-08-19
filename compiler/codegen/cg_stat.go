package codegen

import . "golua/compiler/ast"

func cgStat(fi *funcInfo, node Statement) {
	switch stat := node.(type) {
	case *FunctionCallStatement:
		cgFuncCallStat(fi, stat)
	case *BreakStatement:
		cgBreakStat(fi, stat)
	case *DoStatement:
		cgDoStat(fi, stat)
	case *WhileStatement:
		cgWhileStat(fi, stat)
	case *RepeatStatement:
		cgRepeatStat(fi, stat)
	case *IfStatement:
		cgIfStat(fi, stat)
	case *ForNumStatement:
		cgForNumStat(fi, stat)
	case *ForInStatement:
		cgForInStat(fi, stat)
	case *AssignStatement:
		cgAssignStat(fi, stat)
	case *LocalVariableDeclareStatement:
		cgLocalVarDeclStat(fi, stat)
	case *LocalFunctionDefineStatement:
		cgLocalFuncDefStat(fi, stat)
	case *LabelStatement, *GotoStatement:
		panic("label and goto statements are not supported!")
	}
}

func cgLocalFuncDefStat(fi *funcInfo, node *LocalFunctionDefineStatement) {
	r := fi.addLocalVariable(node.Name, fi.pc()+2)
	cgFuncDefExp(fi, node.Expression, r)
}

func cgFuncCallStat(fi *funcInfo, node *FunctionCallStatement) {
	r := fi.allocReg()
	cgFuncCallExp(fi, node, r, 0)
	fi.freeReg()
}

func cgBreakStat(fi *funcInfo, node *BreakStatement) {
	pc := fi.emitJmp(node.Line, 0, 0)
	fi.addBreakJmp(pc)
}

func cgDoStat(fi *funcInfo, node *DoStatement) {
	fi.enterScope(false)
	cgBlock(fi, node.Block)
	fi.closeOpenUpvals(node.Block.LastLine)
	fi.exitScope(fi.pc() + 1)
}

/*
           ______________
          /  false? jmp  |
         /               |
while exp do block end <-'
      ^           \
      |___________/
           jmp
*/
func cgWhileStat(fi *funcInfo, node *WhileStatement) {
	pcBeforeExp := fi.pc()

	oldRegs := fi.usedRegs
	a, _ := expToOpArg(fi, node.Expression, ARG_REG)
	fi.usedRegs = oldRegs

	line := lastLineOf(node.Expression)
	fi.emitTest(line, a, 0)
	pcJmpToEnd := fi.emitJmp(line, 0, 0)

	fi.enterScope(true)
	cgBlock(fi, node.Block)
	fi.closeOpenUpvals(node.Block.LastLine)
	fi.emitJmp(node.Block.LastLine, 0, pcBeforeExp-fi.pc()-1)
	fi.exitScope(fi.pc())

	fi.fixSbx(pcJmpToEnd, fi.pc()-pcJmpToEnd)
}

/*
        ______________
       |  false? jmp  |
       V              /
repeat block until exp
*/
func cgRepeatStat(fi *funcInfo, node *RepeatStatement) {
	fi.enterScope(true)

	pcBeforeBlock := fi.pc()
	cgBlock(fi, node.Block)

	oldRegs := fi.usedRegs
	a, _ := expToOpArg(fi, node.Expression, ARG_REG)
	fi.usedRegs = oldRegs

	line := lastLineOf(node.Expression)
	fi.emitTest(line, a, 0)
	fi.emitJmp(line, fi.getJmpArgA(), pcBeforeBlock-fi.pc()-1)
	fi.closeOpenUpvals(line)

	fi.exitScope(fi.pc() + 1)
}

/*
         _________________       _________________       _____________
        / false? jmp      |     / false? jmp      |     / false? jmp  |
       /                  V    /                  V    /              V
if exp1 then block1 elseif exp2 then block2 elseif true then block3 end <-.
                   \                       \                       \      |
                    \_______________________\_______________________\_____|
                    jmp                     jmp                     jmp
*/
func cgIfStat(fi *funcInfo, node *IfStatement) {
	pcJmpToEnds := make([]int, len(node.Expressions))
	pcJmpToNextExp := -1

	for i, exp := range node.Expressions {
		if pcJmpToNextExp >= 0 {
			fi.fixSbx(pcJmpToNextExp, fi.pc()-pcJmpToNextExp)
		}

		oldRegs := fi.usedRegs
		a, _ := expToOpArg(fi, exp, ARG_REG)
		fi.usedRegs = oldRegs

		line := lastLineOf(exp)
		fi.emitTest(line, a, 0)
		pcJmpToNextExp = fi.emitJmp(line, 0, 0)

		block := node.Blocks[i]
		fi.enterScope(false)
		cgBlock(fi, block)
		fi.closeOpenUpvals(block.LastLine)
		fi.exitScope(fi.pc() + 1)
		if i < len(node.Expressions)-1 {
			pcJmpToEnds[i] = fi.emitJmp(block.LastLine, 0, 0)
		} else {
			pcJmpToEnds[i] = pcJmpToNextExp
		}
	}

	for _, pc := range pcJmpToEnds {
		fi.fixSbx(pc, fi.pc()-pc)
	}
}

func cgForNumStat(fi *funcInfo, node *ForNumStatement) {
	forIndexVar := "(for index)"
	forLimitVar := "(for limit)"
	forStepVar := "(for step)"

	fi.enterScope(true)

	cgLocalVarDeclStat(fi, &LocalVariableDeclareStatement{
		NameList: []string{forIndexVar, forLimitVar, forStepVar},
		ExpressionList:  []Expression{node.InitExpression, node.LimitExpression, node.StepExpression},
	})
	fi.addLocalVariable(node.VarName, fi.pc()+2)

	a := fi.usedRegs - 4
	pcForPrep := fi.emitForPrep(node.LineOfDo, a, 0)
	cgBlock(fi, node.Block)
	fi.closeOpenUpvals(node.Block.LastLine)
	pcForLoop := fi.emitForLoop(node.LineOfFor, a, 0)

	fi.fixSbx(pcForPrep, pcForLoop-pcForPrep-1)
	fi.fixSbx(pcForLoop, pcForPrep-pcForLoop)

	fi.exitScope(fi.pc())
	fi.fixEndPC(forIndexVar, 1)
	fi.fixEndPC(forLimitVar, 1)
	fi.fixEndPC(forStepVar, 1)
}

func cgForInStat(fi *funcInfo, node *ForInStatement) {
	forGeneratorVar := "(for generator)"
	forStateVar := "(for state)"
	forControlVar := "(for control)"

	fi.enterScope(true)

	cgLocalVarDeclStat(fi, &LocalVariableDeclareStatement{
		//LastLine: 0,
		NameList: []string{forGeneratorVar, forStateVar, forControlVar},
		ExpressionList:  node.ExpressionList,
	})
	for _, name := range node.NameList {
		fi.addLocalVariable(name, fi.pc()+2)
	}

	pcJmpToTFC := fi.emitJmp(node.LineOfDo, 0, 0)
	cgBlock(fi, node.Block)
	fi.closeOpenUpvals(node.Block.LastLine)
	fi.fixSbx(pcJmpToTFC, fi.pc()-pcJmpToTFC)

	line := lineOf(node.ExpressionList[0])
	rGenerator := fi.slotOfLocalVariable(forGeneratorVar)
	fi.emitTForCall(line, rGenerator, len(node.NameList))
	fi.emitTForLoop(line, rGenerator+2, pcJmpToTFC-fi.pc()-1)

	fi.exitScope(fi.pc() - 1)
	fi.fixEndPC(forGeneratorVar, 2)
	fi.fixEndPC(forStateVar, 2)
	fi.fixEndPC(forControlVar, 2)
}

func cgLocalVarDeclStat(fi *funcInfo, node *LocalVariableDeclareStatement) {
	exps := removeTailNils(node.ExpressionList)
	nExps := len(exps)
	nNames := len(node.NameList)

	oldRegs := fi.usedRegs
	if nExps == nNames {
		for _, exp := range exps {
			a := fi.allocReg()
			cgExp(fi, exp, a, 1)
		}
	} else if nExps > nNames {
		for i, exp := range exps {
			a := fi.allocReg()
			if i == nExps-1 && isVarargOrFuncCall(exp) {
				cgExp(fi, exp, a, 0)
			} else {
				cgExp(fi, exp, a, 1)
			}
		}
	} else { // nNames > nExps
		multRet := false
		for i, exp := range exps {
			a := fi.allocReg()
			if i == nExps-1 && isVarargOrFuncCall(exp) {
				multRet = true
				n := nNames - nExps + 1
				cgExp(fi, exp, a, n)
				fi.allocRegs(n - 1)
			} else {
				cgExp(fi, exp, a, 1)
			}
		}
		if !multRet {
			n := nNames - nExps
			a := fi.allocRegs(n)
			fi.emitLoadNil(node.LastLine, a, n)
		}
	}

	fi.usedRegs = oldRegs
	startPC := fi.pc() + 1
	for _, name := range node.NameList {
		fi.addLocalVariable(name, startPC)
	}
}

func cgAssignStat(fi *funcInfo, node *AssignStatement) {
	exps := removeTailNils(node.ExpressionList)
	nExps := len(exps)
	nVars := len(node.VariableList)

	tRegs := make([]int, nVars)
	kRegs := make([]int, nVars)
	vRegs := make([]int, nVars)
	oldRegs := fi.usedRegs

	for i, exp := range node.VariableList {
		if taExp, ok := exp.(*TableAccessExpression); ok {
			tRegs[i] = fi.allocReg()
			cgExp(fi, taExp.PrefixExpression, tRegs[i], 1)
			kRegs[i] = fi.allocReg()
			cgExp(fi, taExp.KeyExpression, kRegs[i], 1)
		} else {
			name := exp.(*NameExpression).Name
			if fi.slotOfLocalVariable(name) < 0 && fi.indexOfUpValue(name) < 0 {
				// global var
				kRegs[i] = -1
				if fi.indexOfConstant(name) > 0xFF {
					kRegs[i] = fi.allocReg()
				}
			}
		}
	}
	for i := 0; i < nVars; i++ {
		vRegs[i] = fi.usedRegs + i
	}

	if nExps >= nVars {
		for i, exp := range exps {
			a := fi.allocReg()
			if i >= nVars && i == nExps-1 && isVarargOrFuncCall(exp) {
				cgExp(fi, exp, a, 0)
			} else {
				cgExp(fi, exp, a, 1)
			}
		}
	} else { // nVars > nExps
		multRet := false
		for i, exp := range exps {
			a := fi.allocReg()
			if i == nExps-1 && isVarargOrFuncCall(exp) {
				multRet = true
				n := nVars - nExps + 1
				cgExp(fi, exp, a, n)
				fi.allocRegs(n - 1)
			} else {
				cgExp(fi, exp, a, 1)
			}
		}
		if !multRet {
			n := nVars - nExps
			a := fi.allocRegs(n)
			fi.emitLoadNil(node.LastLine, a, n)
		}
	}

	lastLine := node.LastLine
	for i, exp := range node.VariableList {
		if nameExp, ok := exp.(*NameExpression); ok {
			varName := nameExp.Name
			if a := fi.slotOfLocalVariable(varName); a >= 0 {
				fi.emitMove(lastLine, a, vRegs[i])
			} else if b := fi.indexOfUpValue(varName); b >= 0 {
				fi.emitSetUpval(lastLine, vRegs[i], b)
			} else if a := fi.slotOfLocalVariable("_ENV"); a >= 0 {
				if kRegs[i] < 0 {
					b := 0x100 + fi.indexOfConstant(varName)
					fi.emitSetTable(lastLine, a, b, vRegs[i])
				} else {
					fi.emitSetTable(lastLine, a, kRegs[i], vRegs[i])
				}
			} else { // global var
				a := fi.indexOfUpValue("_ENV")
				if kRegs[i] < 0 {
					b := 0x100 + fi.indexOfConstant(varName)
					fi.emitSetTabUp(lastLine, a, b, vRegs[i])
				} else {
					fi.emitSetTabUp(lastLine, a, kRegs[i], vRegs[i])
				}
			}
		} else {
			fi.emitSetTable(lastLine, tRegs[i], kRegs[i], vRegs[i])
		}
	}

	// todo
	fi.usedRegs = oldRegs
}
